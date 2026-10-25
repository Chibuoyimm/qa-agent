package httpapi_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chibuoyimm/qa-agent/internal/httpapi"
	"github.com/Chibuoyimm/qa-agent/internal/planner"
	"github.com/Chibuoyimm/qa-agent/internal/qa"
	"github.com/Chibuoyimm/qa-agent/internal/repository"
	"github.com/Chibuoyimm/qa-agent/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTestingWorkspaceHTTP(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	schema := "qa_test_" + hex.EncodeToString(suffix[:])
	if _, err := db.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	isolated, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		isolated.Close()
		if _, err := db.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Error(err)
		}
		db.Close()
	})
	if err := migrations.Apply(ctx, isolated); err != nil {
		t.Fatal(err)
	}
	store := qa.NewStore(isolated, map[string]bool{"http://localhost:4174": true, "http://localhost:4175": true})
	p, err := store.CreateProject(ctx, qa.ProjectInput{Name: "HTTP workspace", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateProject(ctx, qa.ProjectInput{Name: "Other app", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(ctx, p.ID, qa.TaskInput{RequestID: strings.Repeat("a", 32), Prompt: "Check report", Title: "Report", JourneyName: "Reports"})
	if err != nil {
		t.Fatal(err)
	}
	draft := qa.ScenarioInput{Name: "Report", Description: "", ExpectedOutcome: "The report is visible", Steps: []qa.Step{{Action: "navigate", Path: "/report"}, {Action: "assert_visible", TestID: "report"}}}
	calls := 0
	proposals, err := planner.New("fixture", "", transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if !bytes.Contains(raw, []byte("Observed report")) {
			t.Error("task discovery not included in provider context")
		}
		if calls == 2 && !bytes.Contains(raw, []byte("Recorded run results")) {
			t.Error("run results missing from discussion")
		}
		text, _ := json.Marshal(map[string]any{"scenarios": []qa.ScenarioInput{draft}, "questions": []string{}, "assumptions": []string{}})
		quoted, _ := json.Marshal(string(text))
		body := `{"status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":` + string(quoted) + `}]}]}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	app := httpapi.New(store, proposals, repository.New(nil), "operator", "worker", slog.New(slog.NewTextHandler(io.Discard, nil)))
	app.ArtifactDirectory = t.TempDir()
	handler := app.Handler()
	request := func(method, path, token string, body any, rangeHeader string) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-QA-Provider-Key", "fixture-key")
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	root := "/api/projects/" + p.ID
	for _, path := range []string{root + "/tasks", root + "/journeys", root + "/coverage", root + "/tasks/" + task.ID + "/chat", "/api/runs/missing/reviews", "/api/runs/missing/artifacts/missing/0"} {
		for _, token := range []string{"", "worker"} {
			if response := request("GET", path, token, nil, ""); response.Code != 401 {
				t.Fatalf("auth %s: %d", path, response.Code)
			}
		}
	}
	if response := request("GET", "/api/projects/"+other.ID+"/tasks/"+task.ID, "operator", nil, ""); response.Code != 404 {
		t.Fatal("cross-app task exposed", response.Code)
	}
	discovery, err := store.DiscoverTask(ctx, p.ID, task.ID, qa.TaskDiscoveryInput{DiscoveryInput: qa.DiscoveryInput{StartPath: "/", MaxPages: 1}})
	if err != nil {
		t.Fatal(err)
	}
	claimed, lease, _, found, err := store.ClaimDiscovery(ctx, "fixture-worker")
	if err != nil || !found || claimed.ID != discovery.ID {
		t.Fatal(err)
	}
	if _, err := store.CompleteDiscovery(ctx, discovery.ID, qa.CompleteDiscoveryInput{LeaseToken: lease, Result: &qa.DiscoveryResult{Pages: []qa.DiscoveryPage{{Path: "/", Title: "Observed report", Headings: []string{}, Elements: []qa.DiscoveryElement{}, Links: []qa.DiscoveryLink{}}}, Warnings: []string{}}}); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"request_id": strings.Repeat("b", 32), "prompt": "Check report", "provider": "openai", "context": "Independent requirement: report visible", "model": "fixture", "credential_mode": "byok", "consent": true}
	response := request("POST", root+"/tasks/"+task.ID+"/chat", "operator", input, "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var turn qa.ChatTurn
	if err := json.Unmarshal(response.Body.Bytes(), &turn); err != nil {
		t.Fatal(err)
	}
	if turn.TaskID != task.ID || turn.Proposal == nil || turn.Proposal.DiscoveryID != discovery.ID {
		t.Fatalf("missing context %+v", turn)
	}
	approval := qa.TaskApprovalInput{TurnID: turn.ID, Drafts: []qa.ReviewedDraft{{Index: 0, ScenarioInput: draft}}}
	response = request("POST", root+"/tasks/"+task.ID+"/approve", "operator", approval, "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var approved qa.TaskApproval
	if err := json.Unmarshal(response.Body.Bytes(), &approved); err != nil {
		t.Fatal(err)
	}
	response = request("POST", root+"/tasks/"+task.ID+"/runs", "operator", qa.ChatRunInput{RequestID: strings.Repeat("c", 32), Prompt: "Run reviewed checks", Action: "selected", Mode: "advisory", ScenarioIDs: approved.ScenarioIDs}, "")
	if response.Code != 202 {
		t.Fatal(response.Code, response.Body.String())
	}
	run, lease, _, found, err := store.ClaimRun(ctx, "worker")
	if err != nil || !found {
		t.Fatal(err)
	}
	screenshot := []byte("synthetic-screenshot-content")
	if err := os.WriteFile(filepath.Join(app.ArtifactDirectory, "evidence.png"), screenshot, 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, []byte("private-outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(app.ArtifactDirectory, "escape.png")); err != nil {
		t.Fatal(err)
	}
	result := qa.ScenarioResult{ScenarioID: approved.ScenarioIDs[0], Status: "failed", Message: "Report missing", Artifacts: []qa.Artifact{{Kind: "screenshot", Path: "evidence.png"}, {Kind: "screenshot", Path: "escape.png"}, {Kind: "video", Path: "missing.webm"}}}
	if _, err := store.CompleteRun(ctx, run.ID, qa.CompleteInput{LeaseToken: lease, Results: []qa.ScenarioResult{result}}); err != nil {
		t.Fatal(err)
	}
	media := "/api/runs/" + run.ID + "/artifacts/" + result.ScenarioID
	response = request("GET", media+"/0", "operator", nil, "")
	if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), screenshot) || response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("evidence", response.Code, response.Body.String())
	}
	response = request("GET", media+"/0", "operator", nil, "bytes=0-3")
	if response.Code != 206 || response.Body.String() != string(screenshot[:4]) {
		t.Fatal("range support", response.Code)
	}
	for _, index := range []string{"1", "2", "3", "-1", "bad"} {
		if response := request("GET", media+"/"+index, "operator", nil, ""); response.Code != 404 {
			t.Fatal("unsafe/missing evidence", index, response.Code)
		}
	}
	response = request("GET", "/api/runs/"+run.ID+"/artifacts/foreign/0", "operator", nil, "")
	if response.Code != 404 {
		t.Fatal("foreign evidence", response.Code)
	}
	response = request("POST", "/api/runs/"+run.ID+"/recheck", "operator", qa.RecheckInput{RequestID: strings.Repeat("d", 32), BaseURL: "http://localhost:4175", Mode: "advisory"}, "")
	if response.Code != 202 {
		t.Fatal(response.Code, response.Body.String())
	}
	var recheck qa.Run
	if err := json.Unmarshal(response.Body.Bytes(), &recheck); err != nil {
		t.Fatal(err)
	}
	if recheck.SourceRunID != run.ID || recheck.TaskID != task.ID || recheck.Scenarios[0].ExpectedOutcome != draft.ExpectedOutcome {
		t.Fatal("recheck lost approved source")
	}
	input["request_id"] = strings.Repeat("e", 32)
	input["run_context_id"] = run.ID
	response = request("POST", root+"/tasks/"+task.ID+"/chat", "operator", input, "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	input["request_id"] = strings.Repeat("f", 32)
	response = request("POST", "/api/projects/"+other.ID+"/chat", "operator", input, "")
	if response.Code != 404 || calls != 2 {
		t.Fatal("cross-app run shared with provider", response.Code, calls)
	}
	response = request("POST", "/api/runs/"+run.ID+"/reviews", "operator", qa.FindingReview{ScenarioID: result.ScenarioID, Status: "Confirmed issue", Owner: "QA", Reason: "Report required"}, "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	response = request("GET", root+"/coverage", "operator", nil, "")
	if response.Code != 200 || !bytes.Contains(response.Body.Bytes(), []byte(`"status":"failed"`)) {
		t.Fatal("coverage", response.Code, response.Body.String())
	}
}
