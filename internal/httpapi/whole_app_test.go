package httpapi_test

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Chibuoyimm/qa-agent/internal/httpapi"
	"github.com/Chibuoyimm/qa-agent/internal/planner"
	"github.com/Chibuoyimm/qa-agent/internal/qa"
	"github.com/Chibuoyimm/qa-agent/internal/repository"
)

func TestWholeApp(t *testing.T) {
	ctx := context.Background()
	// Serve the same checkout journey the fixture repositories describe.
	const checkout = `<a data-testid="checkout" href="/receipt">Place order</a><a data-testid="track" href="/shipment">Track shipment</a>`
	const receipt = `<strong data-testid="status">Order confirmed</strong>`
	const shipment = `<strong data-testid="shipping">Delivered</strong>`
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, checkout)
		case "/shipment":
			fmt.Fprint(w, shipment)
		case "/receipt":
			fmt.Fprint(w, receipt)
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()
	store := qa.NewStore(boundaryDB(t), map[string]bool{target.URL: true})
	project, err := store.CreateProject(ctx, qa.ProjectInput{Name: "Checkout journeys", BaseURL: target.URL})
	if err != nil {
		t.Fatal(err)
	}
	const prompt = "test the whole app end to end"
	const secret = "test-private-read-credential"
	sha, tree := strings.Repeat("a", 40), strings.Repeat("b", 40)
	blobID := func(content string) string {
		return fmt.Sprintf("%x", sha1.Sum([]byte(fmt.Sprintf("blob %d\x00%s", len(content), content))))
	}
	reply := func(code int, body string) *http.Response {
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body))}
	}
	repo := repository.New(transportFunc(func(r *http.Request) (*http.Response, error) {
		private := strings.Contains(r.URL.Path, "/shop/frontend/")
		if private && r.Header.Get("Authorization") != "Bearer "+secret {
			return reply(404, ""), nil
		}
		if !private && r.Header.Get("Authorization") != "" {
			t.Error("private repository credential crossed repository boundary")
		}
		file, contents := "orders.ts", "Orders must show Order confirmed on /receipt. Track shipment shows Delivered on /shipment."+strings.Repeat(" ", 25000)
		if private {
			file, contents = "checkout.tsx", checkout+receipt+shipment
		}
		blob := blobID(contents)
		const extraFile = "src/shipping.ts"
		extraContents := strings.Repeat(" ", 20000) + "Order confirmed"
		switch {
		case r.URL.Host == "raw.githubusercontent.com":
			if private || r.Header.Get("Authorization") != "" || !strings.Contains(r.URL.Path, "/"+sha+"/") {
				t.Error("raw content must be public and pinned")
			}
			if strings.HasSuffix(r.URL.Path, "/"+extraFile) {
				return reply(200, extraContents), nil
			}
			if strings.HasSuffix(r.URL.Path, "/"+file) {
				return reply(200, contents), nil
			}
		case strings.Contains(r.URL.Path, "/commits/"):
			return reply(200, fmt.Sprintf(`{"sha":%q,"commit":{"tree":{"sha":%q}}}`, sha, tree)), nil
		case strings.Contains(r.URL.Path, "/trees/"):
			entries := fmt.Sprintf(`{"path":%q,"mode":"100644","type":"blob","size":%d,"sha":%q}`, file, len(contents), blob)
			if !private {
				if r.URL.Query().Get("recursive") != "1" {
					t.Error("expected one recursive inventory")
				}
				entries += fmt.Sprintf(`,{"path":%q,"mode":"100644","type":"blob","size":%d,"sha":%q}`, extraFile, len(extraContents), blobID(extraContents))
			}
			return reply(200, fmt.Sprintf(`{"sha":%q,"tree":[%s]}`, tree, entries)), nil
		case strings.Contains(r.URL.Path, "/blobs/"):
			return reply(200, fmt.Sprintf(`{"sha":%q,"encoding":"base64","size":%d,"content":%q}`, blob, len(contents), base64.StdEncoding.EncodeToString([]byte(contents)))), nil
		}
		return nil, fmt.Errorf("unexpected source request %s", r.URL.Path)
	}))
	value := "Order confirmed"
	scenario := qa.ScenarioInput{Name: "Place an order and see its confirmation", ExpectedOutcome: "The receipt says Order confirmed.", Steps: []qa.Step{{Action: "navigate", Path: "/"}, {Action: "click", TestID: "checkout"}, {Action: "assert_text", TestID: "status", Value: &value}}}
	delivery := "Delivered"
	tracking := qa.ScenarioInput{Name: "Track delivery", ExpectedOutcome: "The shipment is Delivered.", Steps: []qa.Step{{Action: "navigate", Path: "/"}, {Action: "click", TestID: "track"}, {Action: "assert_text", TestID: "shipping", Value: &delivery}}}
	model, err := planner.New("fixture", "managed", transportFunc(func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Input string
			Text  struct{ Format struct{ Name string } }
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return nil, err
		}
		if strings.Contains(payload.Input, secret) {
			t.Error("repository token sent to the model")
		}
		file := "orders.ts"
		if strings.Contains(payload.Input, "checkout.tsx") {
			file = "checkout.tsx"
		}
		var output any
		switch payload.Text.Format.Name {
		case "qa_repository_files":
			paths := []string{file}
			if file == "orders.ts" {
				paths = append(paths, "src/shipping.ts")
			}
			output = map[string]any{"paths": paths, "reason": "Checkout flow"}
		case "qa_repository_journeys":
			if strings.Contains(payload.Input, "src/shipping.ts") {
				file = "src/shipping.ts"
			}
			if !strings.Contains(payload.Input, "Order confirmed") {
				t.Error("missing actual source content")
			}
			output = map[string]any{"journeys": []qa.InferredJourney{{Name: "Order confirmation", Evidence: "From / click checkout, then status shows Order confirmed on /receipt", Paths: []string{file}}, {Name: "Track a shipment", Evidence: "From / click track, expect shipping to say Delivered.", Paths: []string{file}}}, "gaps": []string{"Guest cancellation has no documented expected result"}}
		case "qa_whole_app_plan":
			if !strings.Contains(payload.Input, "shop/backend") || !strings.Contains(payload.Input, "shop/frontend") {
				t.Error("planner missing a project repository")
			}
			coverage := [][]string{{"R1.J1", "R2.J1"}, {"R1.J2", "R2.J2"}}
			if strings.Contains(payload.Input, "unrecognised evidence fixture") {
				coverage[0][0] = "R99.J23"
			}
			output = map[string]any{"scenarios": []qa.ScenarioInput{scenario, tracking}, "questions": []string{}, "assumptions": []string{}, "coverage": coverage}
		default:
			return nil, fmt.Errorf("unexpected model operation %s", payload.Text.Format.Name)
		}
		raw, _ := json.Marshal(output)
		data, _ := json.Marshal(string(raw))
		return reply(200, `{"status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":`+string(data)+`}]}]}`), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	service := httpapi.New(store, model, repo, "operator", "worker", slog.New(slog.NewTextHandler(io.Discard, nil)))
	service.RepositoryKey = func() ([]byte, error) { return bytes.Repeat([]byte{88}, 32), nil }
	service.ArtifactDirectory = t.TempDir()
	mux := http.NewServeMux()
	mux.Handle("/api/", service.Handler())
	webDir := os.Getenv("QA_WHOLE_APP_WEB_DIR")
	if webDir == "" {
		webDir = "../../web/dist"
	}
	mux.Handle("/", http.FileServer(http.Dir(webDir)))
	server := httptest.NewServer(mux)
	defer server.Close()
	root := "/api/projects/" + project.ID
	call := func(method, path, credential string, body any, code int) []byte {
		t.Helper()
		data, _ := json.Marshal(body)
		r, err := http.NewRequest(method, server.URL+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer operator")
		if credential != "" {
			r.Header.Set("X-QA-GitHub-Token", credential)
		}
		resp, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != code || bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, strings.ReplaceAll(string(raw), secret, "[redacted]"))
		}
		return raw
	}
	for _, role := range []string{"backend", "frontend"} {
		token := ""
		if role == "frontend" {
			token = secret
		}
		call("POST", root+"/repository-sources", token, map[string]any{"provider": "github", "repository": "shop/" + role, "ref": "main", "role": role, "remember_credentials": token != ""}, 200)
	}
	roots, err := store.ProjectSources(ctx, project.ID)
	if err != nil || len(roots) != 2 || !roots[1].HasSavedAccess || bytes.Contains(roots[1].Credential, []byte(secret)) {
		t.Fatal("expected two connections with private read access encrypted")
	}
	input := map[string]any{"prompt": prompt, "model": "fixture", "credential_mode": "managed", "consent": true, "whole_app": true}
	task, err := store.CreateTask(ctx, project.ID, qa.TaskInput{RequestID: strings.Repeat("b", 32), Prompt: prompt, Title: "All journeys", JourneyName: "Whole app"})
	if err != nil {
		t.Fatal(err)
	}
	taskPath := root + "/tasks/" + task.ID
	batch := strings.Repeat("a", 32)
	input["request_id"] = strings.Repeat("e", 32)
	input["survey_batch"] = batch
	call("POST", taskPath+"/chat", "", input, 400)
	surveyInput := map[string]any{"prompt": prompt, "model": "fixture", "credential_mode": "managed", "consent": false, "whole_app": true}
	for _, source := range roots {
		path := taskPath + "/source-surveys/" + batch + "/" + source.ID
		surveyInput["consent"] = false
		call("POST", path, "", surveyInput, 400)
		surveyInput["consent"] = true
		var scan qa.SourceSurvey
		raw := call("POST", path, "", surveyInput, 200)
		if err := json.Unmarshal(raw, &scan); err != nil {
			t.Fatal(err)
		}
		wantFiles := 1
		if source.Role == "backend" {
			wantFiles = 2
			for i := 0; i < 2; i++ {
				if scan.Status != "scanning" || scan.BatchesRead != i || scan.Batches != 2 {
					t.Fatalf("bad checkpoint: %+v", scan)
				}
				call("POST", taskPath+"/chat", "", input, 400)
				raw = call("POST", path, "", surveyInput, 200)
				if err := json.Unmarshal(raw, &scan); err != nil {
					t.Fatal(err)
				}
			}
		}
		if scan.Status != "live" || scan.CommitSHA != sha || len(scan.Paths) != wantFiles || len(scan.Journeys) != 2*wantFiles {
			t.Fatalf("invalid scan: %s", raw)
		}
		if cached := call("POST", path, "", surveyInput, 200); !bytes.Equal(cached, raw) {
			t.Fatal("retry lost the original survey")
		}
	}
	var drafted qa.ChatTurn
	raw := call("POST", taskPath+"/chat", "", input, 200)
	if err := json.Unmarshal(raw, &drafted); err != nil || drafted.Proposal == nil || drafted.Proposal.WholeApp == nil || len(drafted.Proposal.WholeApp.Sources) != 2 || len(drafted.Proposal.Scenarios) != 2 || drafted.Proposal.Scenarios[0].Approved || drafted.RunID != "" {
		t.Fatalf("unexpected draft: %s", raw)
	}
	if checks, err := store.ListScenarios(ctx, project.ID); err != nil || len(checks) != 0 {
		t.Fatal("drafting persisted runnable checks before approval")
	}
	input["context"] = "unrecognised evidence fixture"
	input["request_id"] = strings.Repeat("2", 32)
	call("POST", taskPath+"/chat", "", input, 502)
	delete(input, "context")
	if os.Getenv("QA_VERIFY_WHOLE_APP_BROWSER") == "1" {
		t.Run("browser approval and worker execution", func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), "node", "../../scripts/whole-app-proof.mjs", server.URL, target.URL, service.ArtifactDirectory)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("browser verification: %v\n%s", err, output)
			}
		})
	}
	call("DELETE", root+"/repository-sources/"+roots[1].ID+"/credential", "", nil, 204)
	path := taskPath + "/source-surveys/" + strings.Repeat("c", 32) + "/" + roots[1].ID
	if raw := call("POST", path, "", surveyInput, 200); !bytes.Contains(raw, []byte(`"status":"unavailable"`)) {
		t.Fatal("revoked access without a snapshot must be visible")
	}
	// A private repo with a previous import uses the saved file list and original revision.
	_, err = store.CreateRepositorySnapshot(ctx, project.ID, repository.Snapshot{Provider: "github", Repository: "shop/frontend", Role: "frontend", Ref: "main", CommitSHA: tree, ContentSHA256: strings.Repeat("d", 64), TotalBytes: len(receipt), Files: []repository.File{{Path: "checkout.tsx", Content: receipt}}})
	if err != nil {
		t.Fatal(err)
	}
	raw = call("POST", taskPath+"/source-surveys/"+strings.Repeat("d", 32)+"/"+roots[1].ID, "", surveyInput, 200)
	var fallback qa.SourceSurvey
	if err := json.Unmarshal(raw, &fallback); err != nil || fallback.Status != "saved" || fallback.CommitSHA != tree {
		t.Fatalf("missing fallback provenance: %s", raw)
	}
	call("DELETE", root+"/repository-sources/"+roots[1].ID, "", nil, 204)
	listed := call("GET", root+"/repository-sources", "", nil, 200)
	if bytes.Contains(listed, []byte("shop/frontend")) {
		t.Fatal("historical snapshot reintroduced a disconnected repository")
	}

	// Restoring a connection on another ref must not read files saved from main.
	call("DELETE", root+"/repository-sources/"+roots[0].ID, "", nil, 204)
	raw = call("POST", root+"/repository-sources", secret, map[string]any{"provider": "github", "repository": "shop/frontend", "ref": "release", "role": "frontend", "remember_credentials": true}, 200)
	var reconnected qa.ProjectSource
	if json.Unmarshal(raw, &reconnected) != nil || reconnected.ID != roots[1].ID {
		t.Fatal("connection identity changed")
	}
	call("DELETE", root+"/repository-sources/"+reconnected.ID+"/credential", "", nil, 204)
	emptyBatch := strings.Repeat("f", 32)
	call("POST", taskPath+"/source-surveys/"+emptyBatch+"/"+reconnected.ID, "", surveyInput, 200)
	input["survey_batch"] = emptyBatch
	input["request_id"] = strings.Repeat("f", 32)
	raw = call("POST", taskPath+"/chat", "", input, 200)
	var empty qa.ChatTurn
	if json.Unmarshal(raw, &empty) != nil || empty.Proposal == nil || len(empty.Proposal.Scenarios) != 0 || len(empty.Proposal.Questions) == 0 || empty.Proposal.WholeApp.Sources[0].Status != "unavailable" {
		t.Fatalf("unreadable repos should produce a reviewable gap: %s", raw)
	}

}
