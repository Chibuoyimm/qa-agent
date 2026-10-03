package planner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Chibuoyimm/qa-agent/internal/repository"
)

func TestFileSelectionRejectsInventedDuplicateAndOversizedPaths(t *testing.T) {
	for _, output := range []string{
		`{"paths":["src/login.ts"],"reason":"Login implementation"}`,
		`{"paths":[],"reason":"No matching feature"}`,
		`{"paths":["invented.ts"],"reason":"Missing"}`,
		`{"paths":["src/login.ts","src/login.ts"],"reason":"Duplicate"}`,
		`{"paths":["src/login.ts","src/api.ts"],"reason":"Too large"}`,
		`{"paths":["src/login.ts"],"reason":"ok","scenarios":[]}`,
	} {
		p, err := New("test-model", "managed-secret", roundTripFunc(func(req *http.Request) (*http.Response, error) {
			var body providerRequest
			if json.NewDecoder(req.Body).Decode(&body) != nil || body.Text.Format.Name != "qa_repository_files" || !strings.Contains(body.Instructions, "Choose only listed paths") || strings.Contains(body.Input, "unreviewed-content") {
				t.Fatal("incorrect file selection contract")
			}
			return providerReply("completed", output), nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		in := validInput("managed")
		in.Context = "unreviewed-content"
		inventory := repository.Inventory{CommitSHA: strings.Repeat("a", 40), Files: []repository.Candidate{{Path: "src/login.ts", Size: 25000}, {Path: "src/api.ts", Size: 20000}}}
		result, err := p.SelectFiles(context.Background(), in, "", inventory)
		valid := strings.Contains(output, "Login implementation") || strings.Contains(output, "No matching feature")
		if valid && (err != nil || result.Paths == nil) {
			t.Fatalf("valid selection failed: %v", err)
		}
		if !valid && !errors.Is(err, ErrUpstream) {
			t.Fatalf("accepted invalid selection %s: %v", output, err)
		}
		in.Consent = false
		if _, err := p.SelectFiles(context.Background(), in, "", inventory); !errors.Is(err, ErrInvalid) {
			t.Fatalf("missing consent: %v", err)
		}
	}
}

func TestFileSelectionUsesAnthropicAndGoogleContracts(t *testing.T) {
	for _, provider := range []string{"anthropic", "google"} {
		p, err := NewProviders([]ProviderSettings{{Provider: provider, Models: "test-model", ManagedKey: "managed-secret"}}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
			var schema json.RawMessage
			if provider == "anthropic" {
				var body anthropicRequest
				if json.NewDecoder(req.Body).Decode(&body) != nil || body.System != fileInstructions {
					t.Fatal("Anthropic file selection instructions")
				}
				schema = body.OutputConfig.Format.Schema
			} else {
				var body geminiRequest
				if json.NewDecoder(req.Body).Decode(&body) != nil || body.SystemInstruction.Parts[0].Text != fileInstructions {
					t.Fatal("Google file selection instructions")
				}
				schema = body.GenerationConfig.ResponseFormat.Text.Schema
			}
			if string(schema) != string(fileSchema) {
				t.Fatal("file schema replaced by proposal schema")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(adapterReply(provider, `{"paths":["login.ts"],"reason":"Login feature"}`)))}, nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		in := validInput("managed")
		in.Provider = provider
		got, err := p.SelectFiles(context.Background(), in, "", repository.Inventory{Files: []repository.Candidate{{Path: "login.ts", Size: 5}}})
		if err != nil || len(got.Paths) != 1 || got.Paths[0] != "login.ts" {
			t.Fatalf("%s file selection: %+v %v", provider, got, err)
		}
	}
}

func TestChatGPTFileSelectionWire(t *testing.T) {
	output := `{"paths":["login.ts"],"reason":"Login feature"}`
	p, err := New("test-model", "", roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body struct {
			Instructions string       `json:"instructions"`
			Text         responseText `json:"text"`
		}
		if json.NewDecoder(req.Body).Decode(&body) != nil || body.Instructions != fileInstructions || body.Text.Format.Name != "qa_repository_files" || string(body.Text.Format.Schema) != string(fileSchema) {
			t.Fatal("ChatGPT file selection contract")
		}
		encoded, _ := json.Marshal(output)
		data := `{"type":"response.completed","response":{"id":"fixture","status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":` + string(encoded) + `}]}]}}`
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("event: response.completed\ndata: " + data + "\n\n"))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	response, err := p.streamSubscription(context.Background(), validInput("chatgpt"), "subscription-token", draftSpec{fileInstructions, "qa_repository_files", fileSchema})
	if err != nil {
		t.Fatal(err)
	}
	got, err := openAIOutput(response)
	if err != nil || got != output {
		t.Fatalf("ChatGPT files: %s %v", got, err)
	}
}
