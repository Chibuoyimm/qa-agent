package planner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chibuoyimm/qa-agent/internal/chatgpt"
)

func completedEvent(t *testing.T) string {
	t.Helper()
	response := providerReply("completed", validOutput)
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + string(body) + "}\n\n"
}

func TestSubscriptionStreamBoundaries(t *testing.T) {
	completed := completedEvent(t)
	delta := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"
	for _, tc := range []struct {
		name, body string
		want       error
	}{
		{"completed after delta", delta + completed, nil},
		{"completed", completed, nil},
		{"partial text without completed", delta, ErrUpstream},
		{"missing terminal delimiter", strings.TrimSuffix(completed, "\n\n"), ErrUpstream},
		{"incomplete", "data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\"}}\n\n", ErrUpstream},
		{"usage limit after text", delta + "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"subscription_sharing_usage_limit_exceeded\"}}}\n\n", chatgpt.ErrUsageLimit},
		{"unavailable", "data: {\"type\":\"error\",\"code\":\"subscription_sharing_usage_unavailable\"}\n\n", ErrSubscriptionUnavailable},
		{"malformed", "data: broken\n\n", ErrUpstream},
		{"mismatched event", strings.Replace(completed, "event: response.completed", "event: response.failed", 1), ErrUpstream},
		{"wrong terminal status", strings.Replace(completed, "\"status\":\"completed\"", "\"status\":\"incomplete\"", 1), ErrUpstream},
		{"oversized event", "data: " + strings.Repeat("x", 2<<20) + "\n\n" + completed, ErrUpstream},
		{"oversized stream", strings.Repeat(": heartbeat\n\n", 180000) + completed, ErrUpstream},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := readSubscriptionStream(strings.NewReader(tc.body))
			if tc.want == nil {
				if err != nil || result.Status != "completed" || len(result.Output) != 1 {
					t.Fatalf("result: %+v, error %v", result, err)
				}
			} else if !errors.Is(err, tc.want) {
				t.Fatalf("wanted %v got %v", tc.want, err)
			}
		})
	}
}

func leanSubscriptionEvents(t *testing.T) (string, string, string) {
	t.Helper()
	response := providerReply("completed", validOutput)
	var full providerResponse
	if err := json.NewDecoder(response.Body).Decode(&full); err != nil {
		t.Fatal(err)
	}
	full.Output[0].ID = "message_fixture"
	item, err := json.Marshal(full.Output[0])
	if err != nil {
		t.Fatal(err)
	}
	created := `data: {"type":"response.created","response":{"id":"response_fixture","status":"in_progress"}}` + "\n\n"
	done := `data: {"type":"response.output_item.done","output_index":0,"item":` + string(item) + "}\n\n"
	completed := `data: {"type":"response.completed","response":{"id":"response_fixture","status":"completed","output":[]}}` + "\n\n"
	return created, done, completed
}

func TestSubscriptionStreamCollectsFinishedItemsBeforeCompletion(t *testing.T) {
	created, done, completed := leanSubscriptionEvents(t)
	var doneEvent struct {
		Item json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(done, "data:"))), &doneEvent); err != nil {
		t.Fatal(err)
	}
	fullCompleted := strings.Replace(completed, `"output":[]`, `"output":[`+string(doneEvent.Item)+`]`, 1)
	for _, tc := range []struct {
		name, body string
		want       error
	}{
		{"lean completed response", created + done + completed, nil},
		{"matching terminal items", created + done + fullCompleted, nil},
		{"item without terminal event", created + done, ErrUpstream},
		{"empty terminal without item", created + completed, ErrUpstream},
		{"missing response identity", done + completed, ErrUpstream},
		{"wrong response identity", created + done + strings.Replace(completed, "response_fixture", "other_response", 1), ErrUpstream},
		{"duplicate item", created + done + done + completed, ErrUpstream},
		{"missing output index", created + strings.Replace(done, `"output_index":0,`, "", 1) + completed, ErrUpstream},
		{"skipped output index", created + strings.Replace(done, `"output_index":0`, `"output_index":1`, 1) + completed, ErrUpstream},
		{"unknown output type", created + strings.Replace(done, `"type":"message"`, `"type":"function_call"`, 1) + completed, ErrUpstream},
		{"mismatched terminal items", created + done + completedEvent(t), ErrUpstream},
		{"quota after finished item", created + done + `data: {"type":"response.failed","response":{"error":{"code":"subscription_sharing_usage_limit_exceeded"}}}` + "\n\n", chatgpt.ErrUsageLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := readSubscriptionStream(strings.NewReader(tc.body))
			if tc.want == nil {
				if err != nil || result.Status != "completed" || len(result.Output) != 1 || result.Output[0].Content[0].Text != validOutput {
					t.Fatalf("result %+v error %v", result, err)
				}
			} else if !errors.Is(err, tc.want) {
				t.Fatalf("wanted %v got %v", tc.want, err)
			}
		})
	}
}

func TestSubscriptionAcceptsMissingContentTypeOnlyForValidStream(t *testing.T) {
	created, done, completed := leanSubscriptionEvents(t)
	for _, body := range []string{created + done + completed, `{"status":"completed"}`, created + done} {
		p, err := New("", "", roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		result, err := p.streamSubscription(context.Background(), validInput("chatgpt"), "subscription-token")
		if body == created+done+completed {
			if err != nil || len(result.Output) != 1 {
				t.Fatalf("result %+v error %v", result, err)
			}
		} else if !errors.Is(err, ErrUpstream) {
			t.Fatalf("invalid stream accepted: %v", err)
		}
	}
}

func TestSubscriptionWireContract(t *testing.T) {
	calls := 0
	p, err := New("", "", roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.String() != endpoint || req.Header.Get("Authorization") != "Bearer subscription-token" || req.Header.Get("Accept") != "text/event-stream" {
			t.Fatal("wrong destination or subscription credential")
		}
		var body struct {
			Model        string `json:"model"`
			Instructions string `json:"instructions"`
			Input        []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"input"`
			Store           bool         `json:"store"`
			Stream          bool         `json:"stream"`
			MaxOutputTokens *int         `json:"max_output_tokens"`
			Text            responseText `json:"text"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "test-model" || !body.Stream || body.Store || body.MaxOutputTokens != nil || len(body.Input) != 1 || body.Input[0].Role != "user" || !strings.Contains(body.Input[0].Content, "140000") || body.Instructions != instructions || body.Text.Format.Type != "json_schema" || !body.Text.Format.Strict || !json.Valid(body.Text.Format.Schema) {
			t.Fatalf("invalid subscription request: %+v", body)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream; charset=utf-8"}}, Body: io.NopCloser(strings.NewReader(completedEvent(t)))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.streamSubscription(context.Background(), validInput("chatgpt"), "subscription-token")
	if err != nil || result.Status != "completed" || calls != 1 {
		t.Fatalf("status %s error %v calls %d", result.Status, err, calls)
	}
}

func TestSubscriptionHTTPFailures(t *testing.T) {
	for _, tc := range []struct {
		name              string
		status            int
		contentType, body string
		want              error
	}{
		{"expired token", 401, "application/json", "", chatgpt.ErrReconnect},
		{"permission unavailable", 403, "application/json", "", ErrSubscriptionDenied},
		{"ineligible account", 403, "application/json", `{"error":{"code":"subscription_sharing_user_not_eligible"}}`, ErrSubscriptionDenied},
		{"unsupported capability", 400, "application/json", `{"error":{"code":"subscription_sharing_unsupported_capability"}}`, ErrSubscriptionCapability},
		{"temporary usage failure", 503, "application/json", `{"error":{"code":"subscription_sharing_usage_unavailable"}}`, ErrSubscriptionUnavailable},
		{"quota", 429, "application/json", `{"error":{"code":"subscription_sharing_usage_limit_exceeded"}}`, chatgpt.ErrUsageLimit},
		{"wrong content type", 200, "application/json", `{"status":"completed"}`, ErrUpstream},
		{"redirect", 307, "text/event-stream", "", ErrUpstream},
		{"provider outage", 503, "application/json", `{"error":{"message":"sensitive source text"}}`, ErrSubscriptionUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			p, err := New("", "managed-secret", roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": []string{tc.contentType}, "Location": []string{"https://example.test"}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.streamSubscription(context.Background(), validInput("chatgpt"), "subscription-token")
			if !errors.Is(err, tc.want) || calls != 1 {
				t.Fatalf("wanted %v got %v calls%d", tc.want, err, calls)
			}
		})
	}
}

func TestSubscriptionProposalsUseConnectedAccountAndRemainDrafts(t *testing.T) {
	client, transport := subscriptionFixture(t)
	defer client.Close()
	p, err := New("", "managed-secret-that-must-not-be-used", transport)
	if err != nil {
		t.Fatal(err)
	}
	p.ChatGPT = client
	input := validInput("chatgpt")
	input.ChatGPTProfileID = "a31b37be-f14b-4891-a97e-1c8e297652f6"
	result, err := p.Propose(context.Background(), input, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scenarios) != 1 || result.Scenarios[0].Approved || result.CredentialMode != "chatgpt" || result.ChatGPTProfileID != input.ChatGPTProfileID || result.ContextSHA256 == "" {
		t.Fatalf("result: %+v", result)
	}
	input.Model = "unavailable-model"
	if _, err := p.Propose(context.Background(), input, ""); !errors.Is(err, chatgpt.ErrInvalid) {
		t.Fatalf("unavailable account model accepted: %v", err)
	}
	input.Model = "test-model"
	for _, bad := range []Input{
		{Prompt: input.Prompt, Context: input.Context, Model: input.Model, CredentialMode: "chatgpt", Consent: true},
		{Prompt: input.Prompt, Context: input.Context, Model: input.Model, CredentialMode: "chatgpt", ChatGPTProfileID: input.ChatGPTProfileID, Consent: false},
	} {
		if _, err := p.Propose(context.Background(), bad, ""); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid subscription input: %v", err)
		}
	}
	if _, err := p.Propose(context.Background(), input, "an-api-key"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("API key accepted in subscription mode: %v", err)
	}
	if _, err := client.Disconnect(context.Background(), input.ChatGPTProfileID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Propose(context.Background(), input, ""); !errors.Is(err, chatgpt.ErrInvalid) && !errors.Is(err, chatgpt.ErrReconnect) {
		t.Fatalf("disconnected account accepted: %v", err)
	}
}

func subscriptionFixture(t *testing.T) (*chatgpt.Client, http.RoundTripper) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "credentials")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	state := `{"host_id":"urn:uuid:9abc1bbe-9797-47bf-87bb-983212fe3f5d","active_profile_id":"a31b37be-f14b-4891-a97e-1c8e297652f6","profiles":[{"id":"a31b37be-f14b-4891-a97e-1c8e297652f6","email":"qa@example.test","subject":"qa-subject","client_id":"oaiapp_qa_fixture","access_token":"subscription-token","refresh_token":"synthetic-refresh","scopes":["chatgpt.tokens.use.direct"],"expires_at":"` + time.Now().Add(time.Hour).Format(time.RFC3339) + `"}]}`
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case "https://api.openai.com/v1/models":
			if req.Header.Get("Authorization") != "Bearer subscription-token" {
				t.Fatal("catalog did not use the selected subscription")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"models":[{"slug":"test-model","display_name":"Fixture model","visibility":"list"}]}`))}, nil
		case endpoint:
			if req.Header.Get("Authorization") != "Bearer subscription-token" {
				t.Fatal("proposal did not use the selected subscription")
			}
			created, done, completed := leanSubscriptionEvents(t)
			reasoning := `data: {"type":"response.output_item.done","output_index":0,"item":{"id":"reasoning_fixture","type":"reasoning","content":[]}}` + "\n\n"
			done = strings.Replace(done, `"output_index":0`, `"output_index":1`, 1)
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(created + reasoning + done + completed))}, nil
		case "https://auth.openai.com/.well-known/openid-configuration":
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"issuer":"https://auth.openai.com","revocation_endpoint":"https://auth.openai.com/revoke"}`))}, nil
		case "https://auth.openai.com/revoke":
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
		default:
			t.Fatalf("unexpected subscription destination %s", req.URL.String())
			return nil, errors.New("unexpected destination")
		}
	})
	client, err := chatgpt.New(dir, transport)
	if err != nil {
		t.Fatal(err)
	}
	return client, transport
}
