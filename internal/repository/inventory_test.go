package repository

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestInventoryExcludesUnsafeAndGeneratedFilesWithoutReadingContents(t *testing.T) {
	for _, provider := range []string{"github", "azure"} {
		t.Run(provider, func(t *testing.T) {
			calls := 0
			client := New(roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if strings.Contains(r.URL.Path, "/commits/") {
					if provider == "azure" {
						return response(200, `{"commitId":"`+commitSHA+`","treeId":"`+treeSHA+`"}`), nil
					}
					return response(200, `{"sha":"`+commitSHA+`","commit":{"tree":{"sha":"`+treeSHA+`"}}}`), nil
				}
				if !strings.Contains(r.URL.Path, "/trees/") {
					t.Fatalf("unexpected content read: %s", r.URL)
				}
				entries := `[{"path":"app.tsx","mode":"100644","type":"blob","size":50,"sha":"` + blobSHA + `"},{"path":".env","mode":"100644","type":"blob","size":10,"sha":"` + blobSHA + `"},{"path":"node_modules","mode":"040000","type":"tree","sha":"` + treeSHA + `"},{"path":"logo.png","mode":"100644","type":"blob","size":10,"sha":"` + blobSHA + `"},{"path":"link.ts","mode":"120000","type":"blob","size":5,"sha":"` + blobSHA + `"},{"path":"huge.ts","mode":"100644","type":"blob","size":40001,"sha":"` + blobSHA + `"}]`
				body := `{"sha":"` + treeSHA + `","tree":` + entries + `}`
				if provider == "azure" {
					entries = strings.NewReplacer(`"path":`, `"relativePath":`, `"type":`, `"gitObjectType":`, `"sha":`, `"objectId":`).Replace(entries)
					body = `{"objectId":"` + treeSHA + `","treeEntries":` + entries + `}`
				}
				return response(200, body), nil
			}))
			repo := "owner/repo"
			if provider == "azure" {
				repo = "https://dev.azure.com/org/project/_git/repo"
			}
			got, err := client.Inventory(context.Background(), Input{Provider: provider, Repository: repo, Ref: commitSHA, Role: "frontend"}, "")
			if err != nil || got.CommitSHA != commitSHA || len(got.Files) != 1 || got.Files[0].Path != "app.tsx" || got.Excluded != 5 || calls != 2 {
				t.Fatalf("inventory=%+v calls=%d err=%v", got, calls, err)
			}
		})
	}
}

func TestInventoryRefusesTruncationAndUnboundedTrees(t *testing.T) {
	for _, truncated := range []bool{true, false} {
		client := New(roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if strings.Contains(r.URL.Path, "/commits/") {
				return response(200, `{"sha":"`+commitSHA+`","commit":{"tree":{"sha":"`+treeSHA+`"}}}`), nil
			}
			if truncated {
				return response(200, `{"sha":"`+treeSHA+`","truncated":true}`), nil
			}
			return response(200, `{"sha":"`+treeSHA+`","tree":[{"path":"recursive","type":"tree","mode":"040000","sha":"`+treeSHA+`"}]}`), nil
		}))
		_, err := client.Inventory(context.Background(), Input{Repository: "owner/repo", Ref: "main", Role: "frontend"}, "")
		want := ErrInventoryLimit
		if truncated {
			want = ErrUpstream
		}
		if !errors.Is(err, want) {
			t.Fatalf("want %v, got %v", want, err)
		}
	}
}
