package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func discoveryEnv(base string) func(string) string {
	return func(key string) string {
		if key == "QA_API_TOKEN" {
			return "test-token"
		}
		if key == "QA_API_BASE_URL" {
			return base
		}
		return ""
	}
}

func TestDiscoverCreatesAndPollsJob(t *testing.T) {
	gets := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing API token")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/projects/p1/discoveries":
			if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
				t.Error("invalid create request")
			}
			var in struct {
				StartPath       string `json:"start_path"`
				MaxPages        int    `json:"max_pages"`
				SetupScenarioID string `json:"setup_scenario_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.StartPath != "/dashboard" || in.MaxPages != 2 || in.SetupScenarioID != "setup1" {
				t.Errorf("wrong discovery input: %+v, %v", in, err)
			}
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"id":"d1","project_id":"p1","start_path":"/dashboard","max_pages":2,"status":"queued","error":""}`)
		case "/api/discoveries/d1":
			if r.Method != http.MethodGet {
				t.Error("poll was not GET")
			}
			gets++
			if gets == 1 {
				fmt.Fprint(w, `{"id":"d1","project_id":"p1","start_path":"/dashboard","max_pages":2,"status":"running","error":""}`)
			} else {
				fmt.Fprint(w, `{"id":"d1","project_id":"p1","start_path":"/dashboard","max_pages":2,"status":"completed","error":"","result":{"pages":[{"path":"/dashboard","title":"Dashboard","headings":["Revenue"],"elements":[{"test_id":"revenue"}],"links":[{"path":"/orders"}],"truncated":false}],"limited":true,"warnings":["Page limit reached"]}}`)
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	var out, errOut bytes.Buffer
	code := execute(context.Background(), []string{"discover", "--project", "p1", "--start-path", "/dashboard", "--max-pages", "2", "--setup-scenario", "setup1", "--poll", "1ms"}, discoveryEnv(server.URL), &out, &errOut)
	if code != 0 || gets != 2 || !strings.Contains(out.String(), "Dashboard (1 controls, 1 links)") || !strings.Contains(out.String(), "test id: revenue") || !strings.Contains(out.String(), "Observation was limited") || errOut.Len() != 0 {
		t.Fatalf("code=%d gets=%d stdout=%q stderr=%q", code, gets, out.String(), errOut.String())
	}
}

func TestDiscoverJSONHasOnlyTerminalJobOnStdout(t *testing.T) {
	const terminal = `{"id":"d1","project_id":"p1","start_path":"/","max_pages":3,"status":"completed","error":"","created_at":"2026-09-26T00:00:00Z","result":{"pages":[{"path":"/","title":"Home","headings":[],"elements":[],"links":[],"truncated":false}],"limited":false,"warnings":[]}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/projects/p1/discoveries" {
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, terminal)
	}))
	defer server.Close()
	var out, errOut bytes.Buffer
	code := execute(context.Background(), []string{"discover", "--project", "p1", "--json"}, discoveryEnv(server.URL), &out, &errOut)
	if code != 0 || out.String() != terminal+"\n" || !strings.Contains(errOut.String(), "Discovery d1: completed") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
}

func TestDiscoverTerminalFailureAndTimeout(t *testing.T) {
	for _, status := range []string{"error", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				fmt.Fprintf(w, `{"id":"d1","project_id":"p1","start_path":"/","max_pages":3,"status":%q,"error":"Discovery failed"}`, status)
			}))
			defer server.Close()
			var out, errOut bytes.Buffer
			code := execute(context.Background(), []string{"discover", "--project", "p1", "--json"}, discoveryEnv(server.URL), &out, &errOut)
			if code != 1 || !json.Valid(bytes.TrimSpace(out.Bytes())) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
			}
		})
	}
	post, get, cancel := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/projects/p1/discoveries":
			post++
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"id":"d1","project_id":"p1","start_path":"/","max_pages":3,"status":"queued","error":""}`)
		case "GET /api/discoveries/d1":
			get++
			fmt.Fprint(w, `{"id":"d1","project_id":"p1","start_path":"/","max_pages":3,"status":"running","error":""}`)
		default:
			cancel++
		}
	}))
	defer server.Close()
	var out, errOut bytes.Buffer
	code := execute(context.Background(), []string{"discover", "--project", "p1", "--poll", "5ms", "--timeout", "100ms"}, discoveryEnv(server.URL), &out, &errOut)
	if code != 2 || post != 1 || get == 0 || cancel != 0 || !strings.Contains(errOut.String(), "remote job may still be active") {
		t.Fatalf("code=%d post=%d get=%d cancel=%d stderr=%q", code, post, get, cancel, errOut.String())
	}
}

func TestDiscoverRejectsInvalidScopeBeforeNetwork(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	for _, args := range [][]string{
		{"discover", "--project", ""},
		{"discover", "--project", "p1", "--start-path", "//evil"},
		{"discover", "--project", "p1", "--start-path", "/x?token=secret"},
		{"discover", "--project", "p1", "--max-pages", "6"},
		{"discover", "--project", "p1", "--setup-scenario", "../other"},
		{"discover", "--project", "p1", "--timeout", "0s"},
	} {
		var out, errOut bytes.Buffer
		if code := execute(context.Background(), args, discoveryEnv(server.URL), &out, &errOut); code != 2 {
			t.Fatalf("args=%v code=%d", args, code)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid scopes caused %d API calls", calls)
	}
}

func TestDiscoverDoesNotForwardTokenOnRedirect(t *testing.T) {
	forwarded := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = true
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	var out, errOut bytes.Buffer
	if code := execute(context.Background(), []string{"discover", "--project", "p1"}, discoveryEnv(server.URL), &out, &errOut); code != 2 || forwarded {
		t.Fatalf("code=%d forwarded=%v", code, forwarded)
	}
}
