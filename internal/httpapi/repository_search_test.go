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

func verifyRepositorySearchHTTP(t *testing.T, store *qa.Store) {
	for _, provider := range []string{"github", "azure"} {
		t.Run(provider, func(t *testing.T) {
			project, err := store.CreateProject(context.Background(), qa.ProjectInput{Name: "File search boundary", BaseURL: "http://localhost:4174"})
			if err != nil {
				t.Fatal(err)
			}
			sha, tree, blob := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
			calls, modelCalls, refReads := 0, 0, 0
			repo := "owner/app"
			ref := "main"
			header := "X-QA-GitHub-Token"
			if provider == "azure" {
				repo = "https://dev.azure.com/org/project/_git/app"
				header = "X-QA-Azure-PAT"
			}
			client := repository.New(transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if provider == "azure" {
					_, pass, ok := r.BasicAuth()
					if !ok || pass != "repo-secret" || r.URL.Host != "dev.azure.com" {
						t.Fatal("incorrect PAT route")
					}
				} else if r.Header.Get("Authorization") != "Bearer repo-secret" || r.URL.Host != "api.github.com" {
					t.Fatal("incorrect repo token route")
				}
				body := "hello"
				switch {
				case strings.Contains(r.URL.Path, "/refs"):
					refReads++
					body = `{"value":[{"name":"refs/heads/main","objectId":"` + sha + `"}]}`
				case strings.Contains(r.URL.Path, "/commits/"):
					if strings.HasSuffix(r.URL.Path, "/main") {
						refReads++
					} else if !strings.HasSuffix(r.URL.Path, "/"+sha) {
						t.Fatal("import did not pin selected commit")
					}
					if provider == "azure" {
						body = `{"commitId":"` + sha + `","treeId":"` + tree + `"}`
					} else {
						body = `{"sha":"` + sha + `","commit":{"tree":{"sha":"` + tree + `"}}}`
					}
				case strings.Contains(r.URL.Path, "/trees/"):
					if provider == "azure" {
						body = `{"objectId":"` + tree + `","treeEntries":[{"relativePath":"login.ts","mode":"100644","gitObjectType":"blob","size":5,"objectId":"` + blob + `"}]}`
					} else {
						body = `{"sha":"` + tree + `","tree":[{"path":"login.ts","mode":"100644","type":"blob","size":5,"sha":"` + blob + `"}]}`
					}
				case strings.Contains(r.URL.Path, "/blobs/"):
					if provider == "github" {
						body = `{"sha":"` + blob + `","encoding":"base64","size":5,"content":"aGVsbG8="}`
					}
				default:
					t.Fatalf("unexpected repository request %s", r.URL)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			}))
			output := `{"paths":["login.ts"],"reason":"Login implementation"}`
			p, err := planner.New("fixture", "model-secret", transportFunc(func(r *http.Request) (*http.Response, error) {
				modelCalls++
				var body struct {
					Input string `json:"input"`
					Text  struct {
						Format struct {
							Name string `json:"name"`
						} `json:"format"`
					} `json:"text"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || body.Text.Format.Name != "qa_repository_files" || !strings.Contains(body.Input, "login.ts") || strings.Contains(body.Input, "repo-secret") || strings.Contains(body.Input, "hello") {
					t.Fatal("model must get inventory only")
				}
				encoded, _ := json.Marshal(output)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":` + string(encoded) + `}]}]}`))}, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			handler := httpapi.New(store, p, client, "operator", "worker", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
			body := func(consent bool) string {
				data, _ := json.Marshal(map[string]any{"repository": repository.Input{Provider: provider, Repository: repo, Ref: ref, Role: "frontend"}, "ai": planner.Input{Prompt: "Test login", Model: "fixture", CredentialMode: "managed", Consent: consent}})
				return string(data)
			}
			request := func(token, body string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "/api/projects/"+project.ID+"/repositories/search", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+token)
				r.Header.Set(header, "repo-secret")
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				return w
			}
			if got := request("worker", body(true)); got.Code != 401 || calls != 0 {
				t.Fatal("search authorized worker")
			}
			if got := request("operator", body(false)); got.Code != 400 || calls != 0 || modelCalls != 0 {
				t.Fatal("search before consent")
			}
			got := request("operator", body(true))
			var result struct {
				Snapshot   *qa.RepositorySnapshot `json:"snapshot"`
				Candidates int                    `json:"candidates"`
			}
			if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &result) != nil || result.Snapshot == nil {
				t.Fatalf("search failed %d: %s", got.Code, got.Body)
			}
			saved := result.Snapshot
			if saved.CommitSHA != sha || saved.Ref != "main" || len(saved.Files) != 1 || saved.Files[0].Content != "hello" || result.Candidates != 1 || modelCalls != 1 || refReads != 1 {
				t.Fatalf("selection provenance failed %+v", result)
			}
			loaded, err := store.GetRepositorySnapshot(context.Background(), project.ID, saved.ID)
			if err != nil || loaded.ContentSHA256 != saved.ContentSHA256 {
				t.Fatal("search result not persisted")
			}
			output = `{"paths":[],"reason":"No evidence"}`
			if got := request("operator", body(true)); got.Code != 200 || !strings.Contains(got.Body.String(), `"snapshot":null`) {
				t.Fatalf("empty selection: %d %s", got.Code, got.Body)
			}
			output = `{"paths":["invented.ts"],"reason":"invalid"}`
			if got := request("operator", body(true)); got.Code != 502 {
				t.Fatalf("invented model path accepted: %d", got.Code)
			}
			snapshots, err := store.ListRepositorySnapshots(context.Background(), project.ID)
			if err != nil || len(snapshots) != 1 {
				t.Fatal("failed/empty selections saved snapshots")
			}
		})
	}
}
