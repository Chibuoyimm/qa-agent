package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func adapterReply(provider, output string) string {
	text, _ := json.Marshal(output)
	if provider == "anthropic" {
		return `{"type":"message","role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":` + string(text) + `}]}`
	}
	return `{"candidates":[{"index":0,"finishReason":"STOP","content":{"role":"model","parts":[{"text":` + string(text) + `}]}}]}`
}

func TestAdditionalProviderWireContracts(t *testing.T) {
	schema, err := schemaFile.ReadFile("schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, schema); err != nil {
		t.Fatal(err)
	}
	wantSchema := compact.String()
	for _, provider := range []string{"anthropic", "google"} {
		for _, mode := range []string{"byok", "managed"} {
			t.Run(provider+"/"+mode, func(t *testing.T) {
				calls := 0
				workspaceID := ""
				if provider == "anthropic" {
					workspaceID = "wrkspc_managed"
				}
				p, err := NewProviders([]ProviderSettings{
					{Provider: "openai", Models: "openai-only", ManagedKey: "unused-openai-key"},
					{Provider: provider, Models: "test-model,test-model,second-model", ManagedKey: "managed-secret", WorkspaceID: workspaceID},
				}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
					calls++
					if req.Method != "POST" || req.Header.Get("Content-Type") != "application/json" || req.URL.RawQuery != "" {
						t.Fatal("invalid HTTP contract")
					}
					key := "byok-secret"
					if mode == "managed" {
						key = "managed-secret"
					}
					if provider == "anthropic" {
						if req.URL.String() != "https://api.anthropic.com/v1/messages" || req.Header.Get("Authorization") != "Bearer "+key || req.Header.Get("Anthropic-Version") != "2023-06-01" || req.Header.Get("X-Goog-Api-Key") != "" {
							t.Fatal("Anthropic routing or credentials")
						}
						workspace := "wrkspc_byok"
						if mode == "managed" {
							workspace = "wrkspc_managed"
						}
						if req.Header.Get("Anthropic-Workspace-Id") != workspace {
							t.Fatal("workspace credential routing")
						}
						var body anthropicRequest
						dec := json.NewDecoder(req.Body)
						dec.DisallowUnknownFields()
						if err := dec.Decode(&body); err != nil {
							t.Fatal(err)
						}
						if body.Model != "test-model" || body.MaxTokens != 6000 || body.System != instructions || len(body.Messages) != 1 || body.Messages[0].Role != "user" || body.Messages[0].Content != "Testing request:\nCheck revenue\n\nApplication context:\nRevenue test ID is revenue; expected net 140000" || body.OutputConfig.Format.Type != "json_schema" || string(body.OutputConfig.Format.Schema) != wantSchema {
							t.Fatal("Anthropic generation contract")
						}
					} else {
						if req.URL.String() != "https://generativelanguage.googleapis.com/v1beta/models/test-model:generateContent" || req.Header.Get("X-Goog-Api-Key") != key || req.Header.Get("Authorization") != "" || req.Header.Get("Anthropic-Workspace-Id") != "" {
							t.Fatal("Gemini routing or credentials")
						}
						var body geminiRequest
						dec := json.NewDecoder(req.Body)
						dec.DisallowUnknownFields()
						if err := dec.Decode(&body); err != nil {
							t.Fatal(err)
						}
						if body.GenerationConfig.MaxOutputTokens != 6000 || body.GenerationConfig.CandidateCount != 1 || body.GenerationConfig.ResponseFormat.Text.MIMEType != "application/json" || string(body.GenerationConfig.ResponseFormat.Text.Schema) != wantSchema || len(body.SystemInstruction.Parts) != 1 || body.SystemInstruction.Parts[0].Text != instructions || len(body.Contents) != 1 || body.Contents[0].Role != "user" || len(body.Contents[0].Parts) != 1 || body.Contents[0].Parts[0].Text != "Testing request:\nCheck revenue\n\nApplication context:\nRevenue test ID is revenue; expected net 140000" {
							t.Fatal("Gemini generation contract")
						}
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(adapterReply(provider, validOutput)))}, nil
				}))
				if err != nil {
					t.Fatal(err)
				}
				in := validInput(mode)
				in.Provider = provider
				if provider == "anthropic" && mode == "byok" {
					in.WorkspaceID = "wrkspc_byok"
				}
				result, err := p.Propose(context.Background(), in, "byok-secret")
				if err != nil {
					t.Fatal(err)
				}
				if calls != 1 || result.Provider != provider || len(result.Scenarios) != 1 || result.Scenarios[0].Approved || result.ContextSHA256 == "" {
					t.Fatalf("result %+v, calls %d", result, calls)
				}
				cfg, err := json.Marshal(p.Config())
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(cfg), "secret") || strings.Contains(string(cfg), "wrkspc_") || len(p.Config().Providers[1].Models) != 2 {
					t.Fatal("public config exposes credentials or duplicate models")
				}
			})
		}
	}
}

func TestAdditionalProvidersRejectUnsafeOutput(t *testing.T) {
	for _, provider := range []string{"anthropic", "google"} {
		reply := adapterReply(provider, validOutput)
		type failureCase struct {
			name, body string
			status     int
			want       error
		}
		cases := []failureCase{
			{"invalid JSON", "broken", 200, ErrUpstream},
			{"invalid proposal", adapterReply(provider, strings.Replace(validOutput, "assert_text", "evaluate", 1)), 200, ErrUpstream},
			{"trailing proposal", adapterReply(provider, validOutput+` {}`), 200, ErrUpstream},
			{"missing arrays", adapterReply(provider, `{"scenarios":[]}`), 200, ErrUpstream},
			{"unknown proposal field", adapterReply(provider, `{"scenarios":[],"questions":["What outcome?"],"assumptions":[],"execute":true}`), 200, ErrUpstream},
			{"oversized", strings.Repeat("x", (2<<20)+1), 200, ErrUpstream},
			{"unauthorized", "key and private context", 401, ErrProviderAuth},
			{"forbidden", "key and private context", 403, ErrProviderAuth},
			{"missing model", "key and private context", 404, ErrProviderAuth},
			{"request", "key and private context", 400, ErrProviderRequest},
			{"schema", "key and private context", 422, ErrProviderRequest},
			{"quota", "key and private context", 429, ErrProviderQuota},
			{"outage", "key and private context", 529, ErrProviderUnavailable},
			{"redirect", "", 307, ErrUpstream},
		}
		if provider == "anthropic" {
			cases = append(cases,
				failureCase{"max tokens", strings.Replace(reply, "end_turn", "max_tokens", 1), 200, ErrUpstream},
				failureCase{"refusal", strings.Replace(reply, "end_turn", "refusal", 1), 200, ErrUpstream},
				failureCase{"tool", strings.Replace(reply, `"type":"text"`, `"type":"tool_use"`, 1), 200, ErrUpstream},
				failureCase{"extra text", strings.Replace(reply, `"content":[`, `"content":[{"type":"text","text":"extra"},`, 1), 200, ErrUpstream},
			)
		} else {
			cases = append(cases,
				failureCase{"max tokens", strings.Replace(reply, "STOP", "MAX_TOKENS", 1), 200, ErrUpstream},
				failureCase{"safety", strings.Replace(reply, "STOP", "SAFETY", 1), 200, ErrUpstream},
				failureCase{"prompt blocked", strings.Replace(reply, `{"candidates"`, `{"promptFeedback":{"blockReason":"SAFETY"},"candidates"`, 1), 200, ErrUpstream},
				failureCase{"blocked rating", strings.Replace(reply, `"index":0`, `"safetyRatings":[{"blocked":true}],"index":0`, 1), 200, ErrUpstream},
				failureCase{"tool part", strings.Replace(reply, `"parts":[{`, `"parts":[{"functionCall":{},`, 1), 200, ErrUpstream},
				failureCase{"reasoning part", strings.Replace(reply, `"parts":[{`, `"parts":[{"thought":true,`, 1), 200, ErrUpstream},
				failureCase{"extra text", strings.Replace(reply, `"parts":[`, `"parts":[{"text":"extra"},`, 1), 200, ErrUpstream},
			)
		}
		for _, tt := range cases {
			t.Run(provider+"/"+tt.name, func(t *testing.T) {
				calls := 0
				p, err := NewProviders([]ProviderSettings{{Provider: provider, Models: "test-model"}}, roundTripFunc(func(*http.Request) (*http.Response, error) {
					calls++
					return &http.Response{StatusCode: tt.status, Header: http.Header{"Location": {"https://example.test/steal"}}, Body: io.NopCloser(strings.NewReader(tt.body))}, nil
				}))
				if err != nil {
					t.Fatal(err)
				}
				in := validInput("byok")
				in.Provider = provider
				_, err = p.Propose(context.Background(), in, "test-secret")
				if !errors.Is(err, tt.want) || calls != 1 || strings.Contains(err.Error(), "private context") || strings.Contains(err.Error(), "test-secret") {
					t.Fatalf("got %v, calls %d", err, calls)
				}
			})
		}
	}
}

func TestProviderConfigurationAndSelection(t *testing.T) {
	for _, model := range []string{"../other", "a/b", "a?key=secret", "a#b", "a:b", "white space", "emoji😀", strings.Repeat("a", 101)} {
		if _, err := NewProviders([]ProviderSettings{{Provider: "google", Models: model}}, nil); err == nil {
			t.Fatalf("accepted unsafe model %q", model)
		}
	}
	for _, settings := range [][]ProviderSettings{
		{{Provider: "unknown"}}, {{Provider: "google"}, {Provider: "google"}}, {{Provider: "google", WorkspaceID: "wrkspc_demo"}}, {{Provider: "anthropic", WorkspaceID: "bad-workspace"}}, {{Provider: "google", ManagedKey: "bad\nkey"}},
	} {
		if _, err := NewProviders(settings, nil); err == nil {
			t.Fatal("accepted invalid config")
		}
	}
	calls := 0
	p, err := NewProviders([]ProviderSettings{{Provider: "anthropic", Models: "test-model"}, {Provider: "google", Models: "gemini-only"}}, roundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected call") }))
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []Input{
		{Provider: "unknown", Prompt: "test", Context: "context", Model: "test-model", CredentialMode: "byok", Consent: true},
		{Provider: "google", Prompt: "test", Context: "context", Model: "test-model", CredentialMode: "byok", Consent: true},
		{Provider: "google", Prompt: "test", Context: "context", Model: "gemini-only", CredentialMode: "byok", WorkspaceID: "wrkspc_demo", Consent: true},
		{Provider: "anthropic", Prompt: "test", Context: "context", Model: "test-model", CredentialMode: "chatgpt", Consent: true},
		{Provider: "anthropic", Prompt: "test", Context: "context", Model: "test-model", CredentialMode: "byok", WorkspaceID: "wrkspc_bad\nheader", Consent: true},
	} {
		if _, err := p.Propose(context.Background(), in, "test-key"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("selection error %v", err)
		}
	}
	if calls != 0 {
		t.Fatal("invalid selection reached provider")
	}
}

func TestLocalProposalLimitsAcrossProviders(t *testing.T) {
	for _, provider := range []string{"anthropic", "google"} {
		for _, kind := range []string{"scenarios", "questions", "assumptions", "steps"} {
			t.Run(provider+"/"+kind, func(t *testing.T) {
				var output proposalOutput
				if err := json.Unmarshal([]byte(validOutput), &output); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "scenarios":
					original := output.Scenarios[0]
					for range 10 {
						output.Scenarios = append(output.Scenarios, original)
					}
				case "questions":
					for range 21 {
						output.Questions = append(output.Questions, "Which expected outcome?")
					}
				case "assumptions":
					for range 21 {
						output.Assumptions = append(output.Assumptions, "Single currency.")
					}
				case "steps":
					original := output.Scenarios[0].Steps[0]
					for range 50 {
						output.Scenarios[0].Steps = append(output.Scenarios[0].Steps, original)
					}
				}
				data, err := json.Marshal(output)
				if err != nil {
					t.Fatal(err)
				}
				p, err := NewProviders([]ProviderSettings{{Provider: provider, Models: "test-model"}}, roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(adapterReply(provider, string(data))))}, nil
				}))
				if err != nil {
					t.Fatal(err)
				}
				in := validInput("byok")
				in.Provider = provider
				if _, err := p.Propose(context.Background(), in, "test-key"); !errors.Is(err, ErrUpstream) {
					t.Fatalf("accepted output beyond %s limit: %v", kind, err)
				}
			})
		}
	}
}
