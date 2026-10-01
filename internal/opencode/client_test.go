package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "opencode" || password != "local-only" {
			t.Error("missing local server authentication")
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return &Client{baseURL: server.URL, password: "local-only", gate: make(chan struct{}, 1), http: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

const fixtureCatalog = `{"all":[{"id":"opencode","models":{"zen-only":{"id":"zen-only","providerID":"opencode","capabilities":{"toolcall":true,"input":{"text":true}}}}},{"id":"opencode-go","key":"never-export-me","models":{"qa-model":{"id":"qa-model","providerID":"opencode-go","name":"QA model","capabilities":{"toolcall":true,"input":{"text":true}}},"no-tools":{"id":"no-tools","providerID":"opencode-go","capabilities":{"input":{"text":true}}},"wrong-provider":{"id":"wrong-provider","providerID":"openai","capabilities":{"toolcall":true,"input":{"text":true}}}}}],"connected":["opencode","opencode-go"]}`
const fixtureResponse = `{"info":{"sessionID":"ses_fixture","role":"assistant","providerID":"opencode-go","modelID":"qa-model","agent":"qa-proposal","finish":"tool-calls","time":{"completed":1},"structured":{"scenarios":[],"questions":["What is the expected result?"],"assumptions":[]}},"parts":[{"type":"tool","tool":"StructuredOutput","state":{"status":"completed"}}]}`

func TestGoOnlyCatalogNeverExposesKeys(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, fixtureCatalog) })
	status, err := c.Status(context.Background())
	if err != nil || !status.Connected || len(status.Models) != 1 || status.Models[0].ID != "qa-model" {
		t.Fatalf("status: %+v err: %v", status, err)
	}
	data, _ := json.Marshal(status)
	if strings.Contains(string(data), "never-export-me") || strings.Contains(string(data), "zen-only") {
		t.Fatal("catalog exported credentials or Zen models")
	}
}

func TestDraftValidatesProviderAndAlwaysCleansOwnedSession(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		want           error
	}{
		{"complete", fixtureResponse, nil},
		{"wrong provider", strings.Replace(fixtureResponse, `"providerID":"opencode-go"`, `"providerID":"opencode"`, 1), ErrUpstream},
		{"wrong model", strings.Replace(fixtureResponse, `"modelID":"qa-model"`, `"modelID":"other"`, 1), ErrUpstream},
		{"unfinished", strings.Replace(fixtureResponse, `"completed":1`, `"completed":0`, 1), ErrUpstream},
		{"external tool", strings.Replace(fixtureResponse, "StructuredOutput", "bash", 1), ErrUpstream},
		{"quota", `{"info":{"error":{"name":"APIError","data":{"statusCode":429,"responseBody":"private upstream data"}}}}`, ErrQuota},
		{"auth", `{"info":{"error":{"name":"ProviderAuthError","data":{"message":"secret"}}}}`, ErrAuth},
		{"structured failure", `{"info":{"error":{"name":"StructuredOutputError"}}}`, ErrUpstream},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cleanup []string
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/provider":
					fmt.Fprint(w, fixtureCatalog)
				case r.URL.Path == "/session":
					var body struct {
						Permission []permissionRule `json:"permission"`
					}
					_ = json.NewDecoder(r.Body).Decode(&body)
					if len(body.Permission) != 2 || body.Permission[0] != (permissionRule{"*", "*", "deny"}) || body.Permission[1] != (permissionRule{"StructuredOutput", "*", "allow"}) {
						t.Error("session has tool permissions")
					}
					fmt.Fprint(w, `{"id":"ses_fixture"}`)
				case strings.HasSuffix(r.URL.Path, "/message"):
					var body struct {
						Model  struct{ ProviderID, ModelID string } `json:"model"`
						Agent  string
						Format struct {
							RetryCount int
							Type       string
						}
						Parts []struct{ Type, Text string }
					}
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body.Model.ProviderID != ProviderID || body.Model.ModelID != "qa-model" || body.Agent != "qa-proposal" || body.Format.RetryCount != 0 || body.Format.Type != "json_schema" || len(body.Parts) != 1 || body.Parts[0].Type != "text" || body.Parts[0].Text != "reviewed evidence" {
						t.Error("wrong model or prompt boundary")
					}
					fmt.Fprint(w, tc.response)
				default:
					cleanup = append(cleanup, r.Method+" "+r.URL.Path)
					fmt.Fprint(w, "true")
				}
			})
			out, err := c.Draft(context.Background(), "qa-model", "QA instructions", "reviewed evidence", json.RawMessage(`{"type":"object"}`))
			if !errors.Is(err, tc.want) || (out != "") != (tc.want == nil) {
				t.Fatalf("output %q err %v", out, err)
			}
			if strings.Join(cleanup, ",") != "POST /session/ses_fixture/abort,DELETE /session/ses_fixture" {
				t.Fatalf("cleanup %v", cleanup)
			}
		})
	}
}

func TestCancellationUsesFreshCleanupContext(t *testing.T) {
	started := make(chan struct{})
	var cleaned atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/provider":
			fmt.Fprint(w, fixtureCatalog)
		case "/session":
			fmt.Fprint(w, `{"id":"ses_fixture"}`)
		case "/session/ses_fixture/message":
			_, _ = io.Copy(io.Discard, r.Body)
			close(started)
			select {
			case <-r.Context().Done():
			case <-time.After(3 * time.Second):
			}
		default:
			cleaned.Add(1)
			fmt.Fprint(w, "true")
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.Draft(ctx, "qa-model", "instructions", "context", json.RawMessage(`{"type":"object"}`))
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if cleaned.Load() != 2 {
		t.Fatal("cancelled session not aborted and deleted")
	}
}

func TestCleanupFailureDoesNotReportSuccessfulDraft(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/provider":
			fmt.Fprint(w, fixtureCatalog)
		case "/session":
			fmt.Fprint(w, `{"id":"ses_fixture"}`)
		case "/session/ses_fixture/message":
			fmt.Fprint(w, fixtureResponse)
		case "/session/ses_fixture/abort":
			fmt.Fprint(w, "true")
		default:
			w.WriteHeader(500)
		}
	})
	out, err := c.Draft(context.Background(), "qa-model", "instructions", "context", json.RawMessage(`{"type":"object"}`))
	if out != "" || !errors.Is(err, ErrCleanup) {
		t.Fatalf("%q %v", out, err)
	}
}

func TestUnlistedModelDoesNotCreateSession(t *testing.T) {
	var calls int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/provider" {
			t.Error("created session for unlisted model")
		}
		fmt.Fprint(w, fixtureCatalog)
	})
	_, err := c.Draft(context.Background(), "zen-only", "instructions", "context", json.RawMessage(`{"type":"object"}`))
	if !errors.Is(err, ErrInvalid) || calls != 1 {
		t.Fatalf("calls %d error %v", calls, err)
	}
}

// This uses the real pinned OpenCode executable and a synthetic external model.
// It proves tool isolation, native headers, protocol and session deletion without
// spending subscription usage. A paid Go account must be verified separately.
func TestNativeRuntimeProposal(t *testing.T) {
	binary := os.Getenv("TEST_OPENCODE_BINARY")
	if binary == "" {
		t.Skip("set TEST_OPENCODE_BINARY to verify the native OpenCode runtime")
	}
	var calls atomic.Int32
	var quota atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := calls.Add(1)
		if count > 2 {
			w.WriteHeader(400)
			return
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected background request: %s", r.URL.Path)
			w.WriteHeader(400)
			return
		}
		if !strings.Contains(r.Header.Get("User-Agent"), "opencode/1.18.34") {
			t.Errorf("client identity %q", r.Header.Get("User-Agent"))
		}
		if r.Header.Get("X-Opencode-Session") == "" {
			t.Error("missing stable native session header")
		}
		if quota.Load() {
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"type":"GoUsageLimitError","message":"Go usage limit reached"},"metadata":{"limitName":"weekly"}}`)
			return
		}
		var body struct {
			Model     string
			MaxTokens int `json:"max_tokens"`
			Tools     []struct{ Function struct{ Name string } }
			Messages  []struct {
				Role    string
				Content json.RawMessage
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Tools) != 1 || body.Tools[0].Function.Name != "StructuredOutput" {
			t.Errorf("external tools enabled: %+v", body.Tools)
			w.WriteHeader(400)
			return
		}
		if body.MaxTokens != 6000 {
			t.Errorf("output cap %d", body.MaxTokens)
		}
		if body.Model != "qa-fixture" {
			t.Errorf("model %q", body.Model)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"id":"fixture","object":"chat.completion.chunk","created":1,"model":"qa-fixture","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_fixture","type":"function","function":{"name":"StructuredOutput","arguments":"{\"questions\":[\"What is the expected outcome?\"]}"}}]},"finish_reason":null}]}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"id":"fixture","object":"chat.completion.chunk","created":1,"model":"qa-fixture","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":10,"total_tokens":30}}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, "data: [DONE]")
		fmt.Fprintln(w)
	}))
	defer upstream.Close()
	var config map[string]any
	_ = json.Unmarshal([]byte(runtimeConfig), &config)
	config["provider"] = map[string]any{ProviderID: map[string]any{"npm": "@ai-sdk/openai-compatible", "options": map[string]any{"baseURL": upstream.URL}, "models": map[string]any{"qa-fixture": map[string]any{"name": "QA fixture", "tool_call": true, "limit": map[string]int{"context": 32000, "output": 12000}}}}}
	encoded, _ := json.Marshal(config)
	directory := filepath.Join(t.TempDir(), "opencode")
	c, err := startRuntime(binary, directory, string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if c != nil {
			c.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	status, err := c.Status(ctx)
	if err != nil || status.Connected || len(status.Models) != 0 {
		t.Fatalf("keyless status: %+v %v", status, err)
	}
	status, err = c.Connect(ctx, "synthetic-go-key")
	if err != nil || !status.Connected {
		t.Fatalf("connection: %+v %v", status, err)
	}
	output, err := c.Draft(ctx, "qa-fixture", "Return a QA proposal from reviewed evidence only.", "Known QA requirements: ask for the missing expected outcome.", json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"questions":{"type":"array","items":{"type":"string"}}},"required":["questions"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if output != `{"questions":["What is the expected outcome?"]}` {
		t.Fatal(output)
	}
	if calls.Load() != 1 {
		t.Fatalf("extra provider requests: %d", calls.Load())
	}
	quota.Store(true)
	_, err = c.Draft(ctx, "qa-fixture", "Return reviewed QA facts.", "synthetic context", json.RawMessage(`{"type":"object"}`))
	if !errors.Is(err, ErrQuota) {
		t.Fatalf("native quota error: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("native quota request retried: %d total calls", calls.Load())
	}
	var sessions []json.RawMessage
	if err := c.call(ctx, http.MethodGet, "/session", nil, &sessions); err != nil || len(sessions) != 0 {
		t.Fatalf("sessions remain: %d %v", len(sessions), err)
	}
	var owned, other struct {
		ID string `json:"id"`
	}
	if err := c.call(ctx, http.MethodPost, "/session", struct {
		Title string `json:"title"`
	}{"QA Agent proposal"}, &owned); err != nil {
		t.Fatal(err)
	}
	if err := c.call(ctx, http.MethodPost, "/session", struct {
		Title string `json:"title"`
	}{"Separate test session"}, &other); err != nil {
		t.Fatal(err)
	}
	c.Close()
	c, err = startRuntime(binary, directory, string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	status, err = c.Status(ctx)
	if err != nil || !status.Connected {
		t.Fatalf("persisted connection: %+v %v", status, err)
	}
	if err := c.call(ctx, http.MethodGet, "/session", nil, &sessions); err != nil || len(sessions) != 1 || !strings.Contains(string(sessions[0]), other.ID) {
		t.Fatalf("recovered sessions: %d %v", len(sessions), err)
	}
	if err := c.cleanupSession(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	status, err = c.Disconnect(ctx)
	if err != nil || status.Connected {
		t.Fatalf("disconnect: %+v %v", status, err)
	}
	c.Close()
	select {
	case <-c.runtime.done:
	default:
		t.Fatal("runtime not stopped")
	}
}

func TestStorageCannotAdoptPersonalData(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "personal-config"), []byte("unrelated data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := claimDirectory(directory); err == nil {
		t.Fatal("adopted existing unrelated directory")
	}
	if _, err := os.Stat(filepath.Join(directory, "qa-agent-runtime")); !os.IsNotExist(err) {
		t.Fatal("created ownership marker for unrelated directory")
	}
	owned := t.TempDir()
	if err := claimDirectory(owned); err != nil {
		t.Fatal(err)
	}
	if err := claimDirectory(owned); err != nil {
		t.Fatal("owned directory could not be reopened", err)
	}
	repository := t.TempDir()
	if err := os.Mkdir(filepath.Join(repository, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	storage := filepath.Join(repository, "credentials")
	if _, err := startRuntime("unused", storage, runtimeConfig); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := os.Stat(storage); !os.IsNotExist(err) {
		t.Fatal("created credentials under Git")
	}
}
