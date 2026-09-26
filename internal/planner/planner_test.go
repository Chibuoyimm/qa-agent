package planner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type blockingBody struct{ ctx context.Context }

func (b blockingBody) Read([]byte) (int, error) { <-b.ctx.Done(); return 0, b.ctx.Err() }
func (blockingBody) Close() error               { return nil }

func providerReply(status, text string) *http.Response {
	content, _ := json.Marshal(struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{Type: "output_text", Text: text})
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"status":"` + status + `","output":[{"type":"message","status":"completed","content":[` + string(content) + `]}]}`))}
}

const validOutput = `{"scenarios":[{"name":"Revenue","description":"Check net","expected_outcome":"Net is 140000","approved":true,"steps":[{"action":"assert_text","path":null,"test_id":"revenue","value":"140000","secret_env":null}]}],"questions":[],"assumptions":[]}`

func validInput(mode string) Input {
	return Input{Prompt: "Check revenue", Context: "Revenue test ID is revenue; expected net 140000", Model: "test-model", CredentialMode: mode, Consent: true}
}

func TestCredentialRoutesAndDrafts(t *testing.T) {
	for _, tt := range []struct{ mode, wantKey string }{{"managed", "managed-secret"}, {"byok", "user-secret"}} {
		t.Run(tt.mode, func(t *testing.T) {
			calls := 0
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.String() != endpoint || req.Header.Get("Authorization") != "Bearer "+tt.wantKey {
					t.Fatalf("provider destination or credential wrong")
				}
				var body struct {
					Model           string `json:"model"`
					Store           bool   `json:"store"`
					MaxOutputTokens int    `json:"max_output_tokens"`
					Text            struct {
						Format struct {
							Type   string          `json:"type"`
							Strict bool            `json:"strict"`
							Schema json.RawMessage `json:"schema"`
						} `json:"format"`
					} `json:"text"`
				}
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.Model != "test-model" || body.Store || body.MaxOutputTokens != 6000 || body.Text.Format.Type != "json_schema" || !body.Text.Format.Strict || !json.Valid(body.Text.Format.Schema) {
					t.Fatalf("provider request: %+v", body)
				}
				return providerReply("completed", validOutput), nil
			})
			planner, err := New("test-model", "managed-secret", transport)
			if err != nil {
				t.Fatal(err)
			}
			key := "user-secret"
			if tt.mode == "managed" {
				key = "ignored-user-key"
			}
			result, err := planner.Propose(context.Background(), validInput(tt.mode), key)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || len(result.Scenarios) != 1 || result.Scenarios[0].Approved || result.ContextSHA256 == "" || result.Provider != "openai" {
				t.Fatalf("result: %+v, calls %d", result, calls)
			}
		})
	}
}

func TestProviderOutputFailures(t *testing.T) {
	cases := []struct {
		name  string
		reply func() *http.Response
	}{
		{"incomplete", func() *http.Response { return providerReply("incomplete", validOutput) }},
		{"malformed", func() *http.Response { return providerReply("completed", "not json") }},
		{"invented action", func() *http.Response {
			return providerReply("completed", strings.Replace(validOutput, "assert_text", "evaluate", 1))
		}},
		{"no scenarios or questions", func() *http.Response {
			return providerReply("completed", `{"scenarios":[],"questions":[],"assumptions":[]}`)
		}},
		{"too many questions", func() *http.Response {
			questions := make([]string, 21)
			for i := range questions {
				questions[i] = "question"
			}
			data, _ := json.Marshal(struct {
				Scenarios   []string `json:"scenarios"`
				Questions   []string `json:"questions"`
				Assumptions []string `json:"assumptions"`
			}{[]string{}, questions, []string{}})
			return providerReply("completed", string(data))
		}},
		{"refusal", func() *http.Response {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"no"}]}]}`))}
		}},
		{"redirect", func() *http.Response {
			return &http.Response{StatusCode: 307, Header: http.Header{"Location": []string{"https://example.test/steal"}}, Body: io.NopCloser(strings.NewReader(""))}
		}},
		{"oversized response", func() *http.Response {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", (2<<20)+1)))}
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			planner, err := New("test-model", "managed-secret", roundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return tt.reply(), nil }))
			if err != nil {
				t.Fatal(err)
			}
			_, err = planner.Propose(context.Background(), validInput("managed"), "")
			if !errors.Is(err, ErrUpstream) || calls != 1 {
				t.Fatalf("expected upstream failure, got %v, calls %d", err, calls)
			}
		})
	}
}

func TestQuestionsWithoutScenarios(t *testing.T) {
	planner, err := New("test-model", "managed-secret", roundTripFunc(func(*http.Request) (*http.Response, error) {
		return providerReply("completed", `{"scenarios":[],"questions":["Which test ID shows revenue?"],"assumptions":[]}`), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := planner.Propose(context.Background(), validInput("managed"), "")
	if err != nil || len(result.Scenarios) != 0 || len(result.Questions) != 1 {
		t.Fatalf("questions only: %+v %v", result, err)
	}
}

func TestValidationBeforeProviderCall(t *testing.T) {
	calls := 0
	planner, err := New("test-model", "", roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return providerReply("completed", validOutput), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if planner.Config().ManagedAvailable || !planner.Config().BYOKAvailable {
		t.Fatal("config availability")
	}
	for _, in := range []Input{
		{Prompt: "", Context: "context", Model: "test-model", CredentialMode: "byok", Consent: true},
		{Prompt: "prompt", Context: "context", Model: "other-model", CredentialMode: "byok", Consent: true},
		{Prompt: "prompt", Context: "context", Model: "test-model", CredentialMode: "byok", Consent: false},
	} {
		if _, err := planner.Propose(context.Background(), in, "key"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid input: %v", err)
		}
	}
	if _, err := planner.Propose(context.Background(), validInput("managed"), "key"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("managed unavailable: %v", err)
	}
	if _, err := planner.Propose(context.Background(), validInput("byok"), ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("byok missing key: %v", err)
	}
	if _, err := planner.Propose(context.Background(), validInput("byok"), strings.Repeat("x", 4097)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("byok oversized key: %v", err)
	}
	disabled, err := New("", "", roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return providerReply("completed", validOutput), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := disabled.Propose(context.Background(), validInput("byok"), "key"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no configured models: %v", err)
	}
	if calls != 0 {
		t.Fatalf("provider called %d times", calls)
	}
}

func TestCancellationTimeoutAndBusy(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		started <- struct{}{}
		select {
		case <-release:
			return providerReply("completed", validOutput), nil
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	})
	planner, err := New("test-model", "managed-secret", transport)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = planner.Propose(context.Background(), validInput("managed"), "") }()
	}
	<-started
	<-started
	if _, err := planner.Propose(context.Background(), validInput("managed"), ""); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy: %v", err)
	}
	close(release)
	wg.Wait()
	blocked, err := New("test-model", "managed-secret", roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	}))
	if err != nil {
		t.Fatal(err)
	}
	blocked.deadline = 10 * time.Millisecond
	_, err = blocked.Propose(context.Background(), validInput("managed"), "")
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("timeout: %v", err)
	}
	readBlocked, err := New("test-model", "managed-secret", roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: blockingBody{ctx: req.Context()}}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	readBlocked.deadline = 10 * time.Millisecond
	_, err = readBlocked.Propose(context.Background(), validInput("managed"), "")
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("body timeout: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = blocked.Propose(ctx, validInput("managed"), "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
