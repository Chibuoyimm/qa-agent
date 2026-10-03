package repository

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

const azureRepo = "https://dev.azure.com/team/Web%20Project/_git/Web%20App"

func TestAzurePinnedImports(t *testing.T) {
	for _, ref := range []string{"feature/login", "refs/heads/main", "refs/tags/v1", commitSHA} {
		t.Run(ref, func(t *testing.T) {
			calls, trees := 0, 0
			client := New(roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				username, password, ok := r.BasicAuth()
				if r.URL.Host != "dev.azure.com" || r.URL.Scheme != "https" || r.URL.Query().Get("api-version") != "7.1" || !ok || username != "" || password != "transient-azure-pat" || r.Header.Get("X-GitHub-Api-Version") != "" {
					t.Fatalf("wrong origin, API version, or credentials")
				}
				if !strings.HasPrefix(r.URL.EscapedPath(), "/team/Web%20Project/_apis/git/repositories/Web%20App/") {
					t.Fatalf("names were not escaped: %s", r.URL.EscapedPath())
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/refs"):
					filter := r.URL.Query().Get("filter")
					if ref == commitSHA || r.URL.Query().Get("peelTags") != "true" || r.URL.Query().Get("$top") != "1000" {
						t.Fatal("unexpected ref lookup")
					}
					if ref == "feature/login" && filter != "heads/feature/login" {
						t.Fatal("bare ref must mean branch")
					}
					objectID := commitSHA
					peeled := ""
					if strings.HasPrefix(ref, "refs/tags/") {
						peeled = `,"peeledObjectId":"` + commitSHA + `"`
						objectID = blobSHA
					}
					return response(200, `{"value":[{"name":"refs/`+filter+`-old","objectId":"`+blobSHA+`"},{"name":"refs/`+filter+`","objectId":"`+objectID+`"`+peeled+`}]}`), nil
				case strings.HasSuffix(r.URL.Path, "/commits/"+commitSHA):
					return response(200, `{"commitId":"`+commitSHA+`","treeId":"`+treeSHA+`"}`), nil
				case strings.HasSuffix(r.URL.Path, "/trees/"+treeSHA):
					trees++
					if r.URL.Query().Get("recursive") != "false" {
						t.Fatal("must use bounded non-recursive trees")
					}
					return response(200, `{"objectId":"`+treeSHA+`","treeEntries":[{"relativePath":"src","mode":"40000","gitObjectType":"tree","objectId":"`+treeSHA+`"},{"relativePath":"app.ts","mode":"100644","gitObjectType":"blob","size":5,"objectId":"`+blobSHA+`","url":"https://attacker.invalid/secret"},{"relativePath":"empty.ts","mode":"100755","gitObjectType":"blob","size":0,"objectId":"`+commitSHA+`"}]}`), nil
				case strings.HasSuffix(r.URL.Path, "/blobs/"+blobSHA):
					if r.URL.Query().Get("$format") != "octetstream" || r.URL.Query().Get("resolveLfs") != "false" || r.Header.Get("Accept") != "application/octet-stream" {
						t.Fatal("must fetch original blob bytes")
					}
					return response(200, "hello"), nil
				case strings.HasSuffix(r.URL.Path, "/blobs/"+commitSHA):
					return response(200, ""), nil
				default:
					t.Fatalf("unexpected request %s", r.URL)
					return nil, nil
				}
			}))
			got, err := client.Fetch(context.Background(), Input{Provider: "azure", Repository: azureRepo, Ref: ref, Role: "backend", Paths: []string{"src/app.ts", "src/empty.ts"}}, "transient-azure-pat")
			if err != nil || got.Provider != "azure" || got.Repository != azureRepo || got.CommitSHA != commitSHA || got.TotalBytes != 5 || len(got.Files) != 2 || got.Files[0].Content != "hello" || got.Files[1].Content != "" || trees != 1 {
				t.Fatalf("unexpected snapshot=%+v err=%v trees=%d", got, err, trees)
			}
			wantCalls := 5
			if ref == commitSHA {
				wantCalls = 4
			}
			if calls != wantCalls {
				t.Fatalf("requests=%d want=%d", calls, wantCalls)
			}
		})
	}
}

func TestAzureRejectsUnsafeURLsBeforeNetwork(t *testing.T) {
	client := New(roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("invalid URL reached network"); return nil, nil }))
	for _, raw := range []string{"http://dev.azure.com/org/project/_git/repo", "https://user:pat@dev.azure.com/org/project/_git/repo", "https://dev.azure.com.evil/org/project/_git/repo", "https://dev.azure.com:443/org/project/_git/repo", "https://org.visualstudio.com/project/_git/repo", "https://dev.azure.com/org/project/_git/repo?secret=pat", "https://dev.azure.com/org/project/_git/repo#fragment", "https://dev.azure.com/org/%2e%2e/_git/repo", "https://dev.azure.com/org/project/_git/repo%2fother", "https://dev.azure.com/org/project/_git/repo/", "https://dev.azure.com/org/project/_git/repo%0a"} {
		_, err := client.Fetch(context.Background(), Input{Provider: "azure", Repository: raw, Ref: "main", Role: "frontend", Paths: []string{"README.md"}}, "pat")
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("URL=%s err=%v", raw, err)
		}
	}
}

func TestAzureRejectsWrongRefsAndUnsafeContent(t *testing.T) {
	for _, tt := range []struct {
		name, refs, commit, tree, blob string
		want                           error
	}{
		{name: "prefix is not exact", refs: `{"value":[{"name":"refs/heads/main-old","objectId":"` + commitSHA + `"}]}`, want: ErrInvalid},
		{name: "bad peeled tag", refs: `{"value":[{"name":"refs/heads/main","objectId":"` + commitSHA + `","peeledObjectId":"invalid"}]}`, want: ErrUpstream},
		{name: "wrong commit", commit: `{"commitId":"` + blobSHA + `","treeId":"` + treeSHA + `"}`, want: ErrUpstream},
		{name: "symlink", tree: `{"objectId":"` + treeSHA + `","treeEntries":[{"relativePath":"README.md","mode":"120000","gitObjectType":"blob","size":5,"objectId":"` + blobSHA + `"}]}`, want: ErrInvalid},
		{name: "submodule", tree: `{"objectId":"` + treeSHA + `","treeEntries":[{"relativePath":"README.md","mode":"160000","gitObjectType":"commit","objectId":"` + blobSHA + `"}]}`, want: ErrInvalid},
		{name: "over size limit", tree: `{"objectId":"` + treeSHA + `","treeEntries":[{"relativePath":"README.md","mode":"100644","gitObjectType":"blob","size":40001,"objectId":"` + blobSHA + `"}]}`, want: ErrInvalid},
		{name: "binary", blob: "he\x00lo", want: ErrInvalid},
		{name: "wrong size", blob: "hell", want: ErrUpstream},
		{name: "overflow", blob: "hello!", want: ErrUpstream},
		{name: "secret", blob: `const password = "private-password"`, want: ErrInvalid},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := New(roundTripFunc(func(r *http.Request) (*http.Response, error) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/refs"):
					if tt.refs != "" {
						return response(200, tt.refs), nil
					}
					return response(200, `{"value":[{"name":"refs/heads/main","objectId":"`+commitSHA+`"}]}`), nil
				case strings.Contains(r.URL.Path, "/commits/"):
					if tt.commit != "" {
						return response(200, tt.commit), nil
					}
					return response(200, `{"commitId":"`+commitSHA+`","treeId":"`+treeSHA+`"}`), nil
				case strings.Contains(r.URL.Path, "/trees/"):
					if tt.tree != "" {
						return response(200, tt.tree), nil
					}
					size := 5
					if tt.name == "secret" {
						size = len(tt.blob)
					}
					return response(200, `{"objectId":"`+treeSHA+`","treeEntries":[{"relativePath":"README.md","mode":"100644","gitObjectType":"blob","size":`+strconv.Itoa(size)+`,"objectId":"`+blobSHA+`"}]}`), nil
				default:
					return response(200, tt.blob), nil
				}
			}))
			_, err := client.Fetch(context.Background(), Input{Provider: "azure", Repository: azureRepo, Ref: "main", Role: "frontend", Paths: []string{"README.md"}}, "")
			if !errors.Is(err, tt.want) {
				t.Fatalf("err=%v want=%v", err, tt.want)
			}
		})
	}
}

func TestAzureProviderFailuresAreBoundedAndRedacted(t *testing.T) {
	for _, status := range []int{401, 403, 404, 203, 302, 303, 429, 503} {
		calls := 0
		client := New(roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			resp := response(status, "upstream body contains transient-azure-pat")
			resp.Header.Set("Location", "https://attacker.invalid/pat")
			return resp, nil
		}))
		_, err := client.Fetch(context.Background(), Input{Provider: "azure", Repository: azureRepo, Ref: "main", Role: "frontend", Paths: []string{"README.md"}}, "transient-azure-pat")
		want := ErrUpstream
		if status == 401 || status == 403 || status == 203 || status == 302 || status == 303 {
			want = ErrAccess
		}
		if !errors.Is(err, want) || calls != 1 || strings.Contains(err.Error(), "transient-azure-pat") {
			t.Fatalf("status=%d err=%v calls=%d", status, err, calls)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := New(roundTripFunc(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() }))
	if _, err := client.Fetch(ctx, Input{Provider: "azure", Repository: azureRepo, Ref: "main", Role: "frontend", Paths: []string{"README.md"}}, ""); !errors.Is(err, ErrTimeout) {
		t.Fatalf("cancelled: %v", err)
	}
}
