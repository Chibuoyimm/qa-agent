package planner

import (
	"errors"
	"testing"

	"github.com/Chibuoyimm/qa-agent/internal/opencode"
)

func TestGoProposalRejectsOtherProviderCredentials(t *testing.T) {
	p, err := New("", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	p.OpenCode = &opencode.Client{}
	valid := Input{Provider: "opencode-go", CredentialMode: "opencode", Prompt: "test reviewed requirements", Context: "known app facts", Model: "go-model", Consent: true}
	if err := p.Validate(valid, ""); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		input Input
		key   string
	}{
		{"OpenAI", func() Input { in := valid; in.Provider = "openai"; return in }(), ""},
		{"Zen", func() Input { in := valid; in.Provider = "opencode"; return in }(), ""},
		{"ChatGPT account", func() Input { in := valid; in.ChatGPTProfileID = "account"; return in }(), ""},
		{"workspace", func() Input { in := valid; in.WorkspaceID = "wrkspc_other"; return in }(), ""},
		{"key header", valid, "unrelated-provider-key"},
		{"model path", func() Input { in := valid; in.Model = "openai/paid-model"; return in }(), ""},
		{"missing consent", func() Input { in := valid; in.Consent = false; return in }(), ""},
		{"BYOK mode", func() Input { in := valid; in.CredentialMode = "byok"; return in }(), "key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := p.Validate(tc.input, tc.key); !errors.Is(err, ErrInvalid) {
				t.Fatal(err)
			}
		})
	}
	p.OpenCode = nil
	if err := p.Validate(valid, ""); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
