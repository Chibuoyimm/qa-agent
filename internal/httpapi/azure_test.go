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
		if !strings.Contains(body.Input, `"provider":"azure"`) || !strings.Contains(body.Input, "hello") || strings.Contains(body.Input, "azure-transient-secret") {
			t.Fatal("source context lost provider/content or leaked PAT")
		}
		text, _ := json.Marshal(`{"scenarios":[],"questions":["What is the expected result?"],"assumptions":[]}`)
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
	denied := repository.New(transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://signin.invalid/"}}, Body: io.NopCloser(strings.NewReader("provider-secret"))}, nil
	}))
	handler = httpapi.New(store, p, denied, "operator", "worker", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
	got = request("POST", base+"/repositories/sync", "operator", body, nil)
	if got.Code != 403 || !strings.Contains(got.Body.String(), "Code (Read)") || strings.Contains(got.Body.String(), "provider-secret") {
		t.Fatalf("access instructions: %d %s", got.Code, got.Body.String())
	}
	listed, err := store.ListRepositorySnapshots(ctx, project.ID)
	if err != nil || len(listed) != 1 {
		t.Fatalf("denied access saved a snapshot: %d %v", len(listed), err)
	}
}
