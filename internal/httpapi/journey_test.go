package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Chibuoyimm/qa-agent/internal/httpapi"
	"github.com/Chibuoyimm/qa-agent/internal/planner"
	"github.com/Chibuoyimm/qa-agent/internal/qa"
	"github.com/Chibuoyimm/qa-agent/internal/repository"
)

func verifyJourneyHTTP(t *testing.T, store *qa.Store) {
	project, err := store.CreateProject(context.Background(), qa.ProjectInput{Name: "Journey planning", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	name, stage := "local_test", "Verify saved state"
	draft := qa.ScenarioInput{Name: "Complete journey", ExpectedOutcome: "The private page is reached", Steps: []qa.Step{{Action: "prepare_fixture", Value: &name}, {Action: "phase", Value: &stage}, {Action: "assert_path", Path: "/private"}, {Action: "cleanup_fixture", Value: &name}}}
	calls := 0
	proposals, err := planner.New("fixture", "managed-fixture", transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) < 300*time.Second {
			t.Error("HTTP boundary cut the complete journey planning budget short")
		}
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), "QA_TEST_EMAIL") || !strings.Contains(string(raw), "local_test") {
			t.Error("selected fixture metadata omitted")
		}
		text, _ := json.Marshal(map[string]any{"scenarios": []qa.ScenarioInput{draft}, "questions": []string{}, "assumptions": []string{}})
		body, _ := json.Marshal(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": string(text)}}}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	service := httpapi.New(store, proposals, repository.New(nil), "operator", "worker", slog.New(slog.NewTextHandler(io.Discard, nil)))
	service.FixtureProfiles = []qa.FixtureProfile{{ID: name, Name: "Local test", Origins: []string{project.BaseURL}, SecretRefs: []string{"QA_TEST_EMAIL"}}}
	handler := service.Handler()
	input := map[string]any{"request_id": strings.Repeat("a", 32), "prompt": "Check the complete journey", "journey": true, "fixture_id": name, "model": "fixture", "credential_mode": "managed", "consent": true, "context": "Known app paths"}
	send := func() *httptest.ResponseRecorder {
		raw, _ := json.Marshal(input)
		req := httptest.NewRequest("POST", "/api/projects/"+project.ID+"/chat", strings.NewReader(string(raw)))
		req.Header.Set("Authorization", "Bearer operator")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	response := send()
	if response.Code != 200 {
		t.Fatalf("journey draft: %d %s", response.Code, response.Body.String())
	}
	var turn qa.ChatTurn
	if err := json.Unmarshal(response.Body.Bytes(), &turn); err != nil || turn.Proposal == nil || len(turn.Proposal.Scenarios) != 1 || turn.Proposal.Scenarios[0].Approved || turn.RunID != "" {
		t.Fatalf("journey must remain an unapproved draft: %v", err)
	}
	input["request_id"] = strings.Repeat("b", 32)
	input["fixture_id"] = "unknown"
	if response = send(); response.Code != 400 || calls != 1 {
		t.Fatalf("unavailable fixture reached the model: %d, calls %d", response.Code, calls)
	}
	service.FixtureProfiles[0].Origins = []string{"http://localhost:4175"}
	input["fixture_id"] = name
	if response = send(); response.Code != 400 || calls != 1 {
		t.Fatalf("fixture crossed its target boundary: %d, calls %d", response.Code, calls)
	}
}
