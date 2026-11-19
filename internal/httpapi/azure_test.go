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

func verifyAzureHTTP(t *testing.T, store *qa.Store) {
	ctx := context.Background()
	project, err := store.CreateProject(ctx, qa.ProjectInput{Name: "Azure boundary", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	sha, tree, blob := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	calls, modelCalls := 0, 0
	var sentContext string
	client := repository.New(transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		_, password, ok := r.BasicAuth()
		if r.URL.Host != "dev.azure.com" || !ok || password != "azure-transient-secret" {
			t.Fatal("wrong credential route")
		}
		body := "hello"
		if strings.Contains(r.URL.Path, "/commits/") {
			body = `{"commitId":"` + sha + `","treeId":"` + tree + `"}`
		}
		if strings.Contains(r.URL.Path, "/trees/") {
			body = `{"objectId":"` + tree + `","treeEntries":[{"relativePath":"README.md","mode":"100644","gitObjectType":"blob","size":5,"objectId":"` + blob + `"}]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	}))
	p, err := planner.New("fixture", "managed-fixture", transportFunc(func(r *http.Request) (*http.Response, error) {
		modelCalls++
		var body struct {
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		sentContext = body.Input
		if !strings.Contains(body.Input, `"provider":"azure"`) || !strings.Contains(body.Input, "hello") || strings.Contains(body.Input, "azure-transient-secret") {
			t.Fatal("source context lost provider/content or leaked PAT")
		}
		text, _ := json.Marshal(`{"scenarios":[{"name":"Review order","description":"An operator sees the submitted order","expected_outcome":"The order is visible","approved":false,"steps":[{"action":"assert_visible","test_id":"order"}]}],"questions":[],"assumptions":[]}`)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":` + string(text) + `}]}]}`))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.New(store, p, client, "operator", "worker", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
	request := func(method, path, token, body string, headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	base := "/api/projects/" + project.ID
	body := `{"provider":"azure","repository":"https://dev.azure.com/team/project/_git/app","ref":"` + sha + `","role":"frontend","paths":["README.md"]}`
	for _, token := range []string{"", "worker"} {
		if got := request("POST", base+"/repositories/sync", token, body, nil); got.Code != 401 {
			t.Fatalf("non-operator import: %d", got.Code)
		}
	}
	for _, input := range []struct {
		body    string
		headers map[string]string
	}{
		{body, map[string]string{"X-QA-GitHub-Token": "github-secret"}},
		{strings.Replace(body, `"azure"`, `"github"`, 1), map[string]string{"X-QA-Azure-PAT": "azure-transient-secret"}},
		{body, map[string]string{"X-QA-GitHub-Token": "github-secret", "X-QA-Azure-PAT": "azure-transient-secret"}},
	} {
		if got := request("POST", base+"/repositories/sync", "operator", input.body, input.headers); got.Code != 400 || calls != 0 {
			t.Fatalf("mismatched credentials: %d calls=%d", got.Code, calls)
		}
	}
	got := request("POST", base+"/repositories/sync", "operator", body, map[string]string{"X-QA-Azure-PAT": "azure-transient-secret"})
	var snapshot qa.RepositorySnapshot
	if got.Code != 201 || json.Unmarshal(got.Body.Bytes(), &snapshot) != nil || snapshot.Provider != "azure" || calls != 3 || strings.Contains(got.Body.String(), "azure-transient-secret") {
		t.Fatalf("Azure sync: %d %s calls=%d", got.Code, got.Body.String(), calls)
	}
	for _, path := range []string{base + "/repositories", base + "/repositories/" + snapshot.ID} {
		got = request("GET", path, "operator", "", nil)
		if got.Code != 200 || !strings.Contains(got.Body.String(), `"provider":"azure"`) || strings.Contains(got.Body.String(), "azure-transient-secret") {
			t.Fatalf("persisted provider: %d %s", got.Code, got.Body.String())
		}
	}
	proposal := `{"request_id":"77777777777777777777777777777777","provider":"openai","prompt":"Test the app","context":"","model":"fixture","credential_mode":"managed","consent":true,"repository_snapshot_ids":["` + snapshot.ID + `"]}`
	got = request("POST", base+"/chat", "operator", proposal, nil)
	if got.Code != 200 || modelCalls != 1 || !strings.Contains(got.Body.String(), `"provider":"azure"`) {
		t.Fatalf("Azure chat source: %d %s", got.Code, got.Body.String())
	}
	if got := request("GET", "/api/projects/missing/repositories/"+snapshot.ID, "operator", "", nil); got.Code != 404 {
		t.Fatalf("cross-project source: %d", got.Code)
	}
	task, err := store.CreateTask(ctx, project.ID, qa.TaskInput{RequestID: strings.Repeat("c", 32), Prompt: "Test cross-app orders", Title: "Order handoff", JourneyName: "Orders"})
	if err != nil {
		t.Fatal(err)
	}
	sources := []qa.RepositorySnapshot{snapshot}
	for _, spec := range []struct{ repo, role string }{{"team/admin", "frontend"}, {"team/orders", "backend"}, {"team/payments", "backend"}} {
		content := "Source for " + spec.repo
		item, err := store.CreateRepositorySnapshot(ctx, project.ID, repository.Snapshot{
			Provider: "github", Repository: spec.repo, Role: spec.role, Ref: "main", CommitSHA: sha, ContentSHA256: strings.Repeat("d", 64),
			Files: []repository.File{{Path: "README.md", Content: content}}, TotalBytes: len(content),
		})
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, item)
	}
	ids := make([]string, 0, len(sources))
	for _, source := range sources {
		ids = append(ids, source.ID)
	}
	payload, err := json.Marshal(struct {
		RequestID string `json:"request_id"`
		planner.Input
	}{
		RequestID: strings.Repeat("8", 32),
		Input:     planner.Input{Provider: "openai", Model: "fixture", CredentialMode: "managed", Prompt: task.Request, Consent: true, RepositorySnapshotIDs: ids},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskChat := base + "/tasks/" + task.ID + "/chat"
	got = request("POST", taskChat, "operator", string(payload), nil)
	if got.Code != http.StatusOK || modelCalls != 2 {
		t.Fatalf("multi-repository plan: %d %s, model calls %d", got.Code, got.Body.String(), modelCalls)
	}
	got = request("GET", taskChat, "operator", "", nil)
	var page qa.ChatPage
	if err := json.Unmarshal(got.Body.Bytes(), &page); err != nil || got.Code != http.StatusOK || len(page.Turns) != 1 || page.Turns[0].Proposal == nil {
		t.Fatalf("read back multi-repository plan: %d %s", got.Code, got.Body.String())
	}
	saved := page.Turns[0].Proposal
	if len(saved.Scenarios) != 1 || len(saved.RepositorySnapshots) != len(sources) {
		t.Fatalf("incomplete saved plan: %+v", saved)
	}
	for i, source := range sources {
		if saved.RepositorySnapshots[i].ID != source.ID || saved.RepositorySnapshots[i].CommitSHA != source.CommitSHA ||
			!strings.Contains(sentContext, source.Repository) || !strings.Contains(sentContext, source.Files[0].Content) {
			t.Fatalf("source missing from the planner input or saved plan: %s", source.Repository)
		}
	}
	denied := repository.New(transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://signin.invalid/"}}, Body: io.NopCloser(strings.NewReader("provider-secret"))}, nil
	}))
	handler = httpapi.New(store, p, denied, "operator", "worker", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
	got = request("POST", base+"/repositories/sync", "operator", body, nil)
	if got.Code != 403 || !strings.Contains(got.Body.String(), "Code (Read)") || strings.Contains(got.Body.String(), "provider-secret") {
		t.Fatalf("access instructions: %d %s", got.Code, got.Body.String())
	}
	listed, err := store.ListRepositorySnapshots(ctx, project.ID)
	if err != nil || len(listed) != len(sources) {
		t.Fatalf("denied access saved a snapshot: %d %v", len(listed), err)
	}
}
