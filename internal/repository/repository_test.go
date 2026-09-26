package repository

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

const (
	commitSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	treeSHA   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	blobSHA   = "cccccccccccccccccccccccccccccccccccccccc"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestFetchPinnedRegularFile(t *testing.T) {
	var paths []string
	client := New(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.github.com" {
			t.Errorf("unexpected destination: %s", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer transient-token" {
			t.Error("token was not sent to GitHub")
		}
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/repos/owner/repo/commits/main":
			return response(200, `{"sha":"`+commitSHA+`","commit":{"tree":{"sha":"`+treeSHA+`"}}}`), nil
		case "/repos/owner/repo/git/trees/" + treeSHA:
			return response(200, `{"sha":"`+treeSHA+`","tree":[{"path":"src","mode":"040000","type":"tree","sha":"`+treeSHA+`"},{"path":"README.md","mode":"100644","type":"blob","size":5,"sha":"`+blobSHA+`"}]}`), nil
		case "/repos/owner/repo/git/blobs/" + blobSHA:
			return response(200, `{"sha":"`+blobSHA+`","encoding":"base64","size":5,"content":"aGVsbG8="}`), nil
		default:
			t.Fatalf("unexpected request: %s", r.URL)
			return nil, nil
		}
	}))
	snapshot, err := client.Fetch(context.Background(), Input{Repository: "owner/repo", Ref: "main", Role: "frontend", Paths: []string{"README.md"}}, "transient-token")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CommitSHA != commitSHA || snapshot.TotalBytes != 5 || len(snapshot.Files) != 1 || snapshot.Files[0].Content != "hello" || len(snapshot.ContentSHA256) != 64 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if len(paths) != 3 {
		t.Fatalf("expected commit, tree, blob requests; got %v", paths)
	}
}

func TestFetchRejectsMissingAndNonRegularPaths(t *testing.T) {
	for _, tt := range []struct {
		name  string
		entry string
	}{
		{"missing", ""},
		{"symlink", `{"path":"README.md","mode":"120000","type":"blob","size":5,"sha":"` + blobSHA + `"}`},
		{"submodule", `{"path":"README.md","mode":"160000","type":"commit","sha":"` + blobSHA + `"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := New(roundTripFunc(func(r *http.Request) (*http.Response, error) {
				switch {
				case strings.Contains(r.URL.Path, "/commits/"):
					return response(200, `{"sha":"`+commitSHA+`","commit":{"tree":{"sha":"`+treeSHA+`"}}}`), nil
				case strings.Contains(r.URL.Path, "/git/trees/"):
					return response(200, `{"sha":"`+treeSHA+`","tree":[`+tt.entry+`]}`), nil
				default:
					t.Fatal("must not read rejected blob")
					return nil, nil
				}
			}))
			_, err := client.Fetch(context.Background(), Input{Repository: "owner/repo", Ref: "main", Role: "backend", Paths: []string{"README.md"}}, "")
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %v, want ErrInvalid", err)
			}
		})
	}
}

func TestRejectUnsafeInputsBeforeNetwork(t *testing.T) {
	client := New(roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("network called for invalid input")
		return nil, nil
	}))
	base := Input{Repository: "owner/repo", Ref: "main", Role: "frontend", Paths: []string{"main.go"}}
	tests := []Input{
		{Repository: "https://github.com/owner/repo", Ref: "main", Role: "frontend", Paths: []string{"main.go"}},
		{Repository: base.Repository, Ref: "../main", Role: base.Role, Paths: base.Paths},
		{Repository: base.Repository, Ref: base.Ref, Role: base.Role, Paths: []string{"../main.go"}},
		{Repository: base.Repository, Ref: base.Ref, Role: base.Role, Paths: []string{".env.local"}},
		{Repository: base.Repository, Ref: base.Ref, Role: base.Role, Paths: []string{"src/main.go", "src/main.go"}},
	}
	for _, input := range tests {
		if _, err := client.Fetch(context.Background(), input, ""); !errors.Is(err, ErrInvalid) {
			t.Fatalf("input %+v: got %v", input, err)
		}
	}
	if unsafeName("credentials.go") || unsafeName("auth.go") || unsafeName("secrets.ts") {
		t.Fatal("ordinary authentication source must remain importable")
	}
}

func TestRejectSecretsAndBinaryButAllowAuthSource(t *testing.T) {
	for _, tt := range []struct {
		content string
		want    bool
	}{
		{`func authenticate(token string) bool { return token != "" }`, true},
		{`const apiKey = "super-secret-value"`, false},
		{`{"apiKey": "super-secret-value"}`, false},
		{`token = "sk-proj-abcdefghijklmnopqrstuvw"`, false},
		{"hello\x00world", false},
		{"hello\xffworld", false},
	} {
		if got := safeText([]byte(tt.content)); got != tt.want {
			t.Errorf("safeText(%q) = %v, want %v", tt.content, got, tt.want)
		}
	}
}

func TestProviderErrorDoesNotLeakToken(t *testing.T) {
	client := New(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("provider error containing transient-token")
	}))
	_, err := client.Fetch(context.Background(), Input{Repository: "owner/repo", Ref: "main", Role: "frontend", Paths: []string{"README.md"}}, "transient-token")
	if !errors.Is(err, ErrUpstream) || strings.Contains(err.Error(), "transient-token") {
		t.Fatalf("provider error leaked: %v", err)
	}
}

func TestRequestAndResponseBudgets(t *testing.T) {
	calls := 0
	client := New(roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return response(200, `{"ok":true}`), nil
	}))
	var target struct {
		OK bool `json:"ok"`
	}
	budget := requestBudget{requests: maxAPIRequests}
	if err := client.get(context.Background(), "https://api.github.com/test", "", 100, &budget, &target); !errors.Is(err, ErrInvalid) || calls != 0 {
		t.Fatalf("request budget: err=%v calls=%d", err, calls)
	}
	budget = requestBudget{bytes: maxMetadataBytes - 2}
	if err := client.get(context.Background(), "https://api.github.com/test", "", 100, &budget, &target); !errors.Is(err, ErrUpstream) || calls != 1 {
		t.Fatalf("byte budget: err=%v calls=%d", err, calls)
	}
}

func TestBoundedResponseAndNoRedirect(t *testing.T) {
	redirect := response(302, "")
	redirect.Header.Set("Location", "https://attacker.example/token")
	for _, tt := range []struct {
		name string
		resp *http.Response
	}{
		{"redirect", redirect},
		{"large", response(200, strings.Repeat("x", 1_000_001))},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			client := New(roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return tt.resp, nil
			}))
			_, err := client.Fetch(context.Background(), Input{Repository: "owner/repo", Ref: "main", Role: "frontend", Paths: []string{"README.md"}}, "")
			if !errors.Is(err, ErrUpstream) || calls != 1 {
				t.Fatalf("got err %v, calls %d", err, calls)
			}
		})
	}
}

func TestOnlyTwoImportsRunConcurrently(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	client := New(roundTripFunc(func(*http.Request) (*http.Response, error) {
		entered <- struct{}{}
		<-release
		return response(503, ""), nil
	}))
	input := Input{Repository: "owner/repo", Ref: "main", Role: "frontend", Paths: []string{"README.md"}}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = client.Fetch(context.Background(), input, "") }()
		<-entered
	}
	if _, err := client.Fetch(context.Background(), input, ""); !errors.Is(err, ErrBusy) {
		t.Fatalf("third import: got %v", err)
	}
	close(release)
	wg.Wait()
}

func TestFileContentLimit(t *testing.T) {
	client := New(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Path, "/commits/"):
			return response(200, `{"sha":"`+commitSHA+`","commit":{"tree":{"sha":"`+treeSHA+`"}}}`), nil
		case strings.Contains(r.URL.Path, "/git/trees/"):
			return response(200, `{"sha":"`+treeSHA+`","tree":[{"path":"large.txt","mode":"100644","type":"blob","size":40001,"sha":"`+blobSHA+`"}]}`), nil
		default:
			t.Fatal("oversized blob must not be fetched")
			return nil, nil
		}
	}))
	_, err := client.Fetch(context.Background(), Input{Repository: "owner/repo", Ref: "main", Role: "frontend", Paths: []string{"large.txt"}}, "")
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v", err)
	}
}
