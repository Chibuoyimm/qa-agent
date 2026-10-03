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

	"github.com/Chibuoyimm/qa-agent/internal/httpapi"
	"github.com/Chibuoyimm/qa-agent/internal/planner"
	"github.com/Chibuoyimm/qa-agent/internal/qa"
	"github.com/Chibuoyimm/qa-agent/internal/repository"
)

func verifyChatHTTP(t *testing.T, store *qa.Store) {
	ctx := context.Background()
	project, err := store.CreateProject(ctx, qa.ProjectInput{Name: "chat HTTP", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	p, err := planner.New("fixture", "", transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		var input struct {
			Input string `json:"input"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			t.Fatal(err)
		}
		if calls == 2 && (!strings.Contains(input.Input, "What should the login show?") || !strings.Contains(input.Input, "Test the login feature")) {
			t.Fatal("follow-up omitted prior question and request")
		}
		text, _ := json.Marshal(`{"scenarios":[],"questions":["What should the login show?"],"assumptions":[]}`)
		body := `{"status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":` + string(text) + `}]}]}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.New(store, p, repository.New(nil), "operator", "worker", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
	request := func(method, path, token, body string, key bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		if key {
			req.Header.Set("X-QA-Provider-Key", "transient-fixture-key")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	path := "/api/projects/" + project.ID + "/chat"
	for _, route := range []string{path, path + "/runs"} {
		for _, token := range []string{"", "worker"} {
			response := request("POST", route, token, `{}`, false)
			if response.Code != 401 {
				t.Fatal("chat endpoint allowed non-operator")
			}
		}
	}
	first := `{"request_id":"11111111111111111111111111111111","provider":"openai","prompt":"Test the login feature","context":"","model":"fixture","credential_mode":"byok","consent":true}`
	response := request("POST", path, "operator", first, true)
	if response.Code != 200 {
		t.Fatalf("first request: %d %s", response.Code, response.Body.String())
	}
	var turn qa.ChatTurn
	if err := json.Unmarshal(response.Body.Bytes(), &turn); err != nil || turn.Proposal == nil || turn.RunID != "" {
		t.Fatalf("draft request executed or failed: %+v %v", turn, err)
	}
	response = request("POST", path, "operator", first, true)
	if response.Code != 200 || calls != 1 {
		t.Fatalf("replay made inference: %d calls %d", response.Code, calls)
	}
	second := strings.ReplaceAll(strings.ReplaceAll(first, "11111111111111111111111111111111", "22222222222222222222222222222222"), "Test the login feature", "It should show the dashboard after valid login")
	response = request("POST", path, "operator", second, true)
	if response.Code != 200 || calls != 2 {
		t.Fatalf("follow-up: %d %s", response.Code, response.Body.String())
	}
	response = request("GET", path, "operator", "", false)
	if response.Code != 200 || strings.Contains(response.Body.String(), "transient-fixture-key") || !strings.Contains(response.Body.String(), "What should the login show?") {
		t.Fatal("history lost response or leaked credential")
	}
	for _, body := range []string{strings.Replace(first, `"consent":true`, `"consent":false`, 1), strings.Replace(first, `"request_id":"11111111111111111111111111111111"`, `"request_id":"bad"`, 1)} {
		response = request("POST", path, "operator", body, true)
		if response.Code != 400 || calls != 2 {
			t.Fatalf("invalid request called provider: %d %d", response.Code, calls)
		}
	}
	// Quota failure closes the persisted turn and never creates a test run or retries.
	failing, err := planner.New("fixture", "", transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader("private upstream body"))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	handler = httpapi.New(store, failing, repository.New(nil), "operator", "worker", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
	response = request("POST", path, "operator", strings.Replace(first, "11111111111111111111111111111111", "33333333333333333333333333333333", 1), true)
	if response.Code != 429 || strings.Contains(response.Body.String(), "private") {
		t.Fatalf("quota response: %d %s", response.Code, response.Body.String())
	}
	page, err := store.ListChat(ctx, project.ID, 0)
	if err != nil || len(page.Turns) != 3 || page.Turns[2].Status != "error" {
		t.Fatalf("failed turn remained pending: %+v %v", page, err)
	}
	cancelledContext, cancel := context.WithCancel(ctx)
	cancelling, err := planner.New("fixture", "", transportFunc(func(r *http.Request) (*http.Response, error) {
		cancel()
		<-r.Context().Done()
		return nil, r.Context().Err()
	}))
	if err != nil {
		t.Fatal(err)
	}
	handler = httpapi.New(store, cancelling, repository.New(nil), "operator", "worker", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
	req := httptest.NewRequest("POST", path, strings.NewReader(strings.Replace(first, "11111111111111111111111111111111", "44444444444444444444444444444444", 1))).WithContext(cancelledContext)
	req.Header.Set("Authorization", "Bearer operator")
	req.Header.Set("X-QA-Provider-Key", "transient-fixture-key")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	page, err = store.ListChat(ctx, project.ID, 0)
	if err != nil || len(page.Turns) != 4 || page.Turns[3].Status != "error" {
		t.Fatalf("cancellation failed to close turn: %+v %v", page, err)
	}

	runs, err := store.ListRuns(ctx, project.ID)
	if err != nil || len(runs) != 0 {
		t.Fatal("drafting executed browser checks")
	}
}
