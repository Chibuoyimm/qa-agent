package planner

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/Chibuoyimm/qa-agent/internal/qa"
)

func TestResolvedPlanUsesOnlyAvailableSetup(t *testing.T) {
	const profile = "verified_users"
	const secret = "QA_TEST_ACCOUNT"
	step := func(action, value string) qa.Step { return qa.Step{Action: action, Value: &value} }
	ref := secret
	draft := qa.ScenarioInput{Name: "Account dashboard", ExpectedOutcome: "Account reaches the dashboard", Steps: []qa.Step{step("prepare_fixture", profile), step("phase", "Sign in"), {Action: "fill", TestID: "user", SecretEnv: &ref}, {Action: "assert_path", Path: "/dashboard"}, step("cleanup_fixture", profile)}}
	raw, _ := json.Marshal(map[string]any{"scenarios": []qa.ScenarioInput{draft}, "assumptions": []string{}, "questions": []string{}})
	p, err := New("test-model", "key", roundTripFunc(func(r *http.Request) (*http.Response, error) { return providerReply("completed", string(raw)), nil }))
	if err != nil {
		t.Fatal(err)
	}
	in := validInput("managed")
	in.ResolveSetup = true
	in.Journey = true
	if _, err = p.Propose(t.Context(), in, ""); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("used an uninstalled setup profile: %v", err)
	}
	in.SetupProfiles = []qa.FixtureProfile{{ID: profile}}
	if _, err = p.Propose(t.Context(), in, ""); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("used an unregistered secret reference: %v", err)
	}
	in.SetupProfiles[0].SecretRefs = []string{secret}
	result, err := p.Propose(t.Context(), in, "")
	if err != nil || len(result.Scenarios) != 1 {
		t.Fatalf("registered profile: %v", err)
	}
}

func TestCompactSetupSchemaCoversWorkerActions(t *testing.T) {
	type fields struct {
		Properties map[string]json.RawMessage `json:"properties"`
		AnyOf      []json.RawMessage          `json:"anyOf"`
		Items      json.RawMessage            `json:"items"`
		Enum       []string                   `json:"enum"`
	}
	decode := func(raw json.RawMessage) fields {
		t.Helper()
		var f fields
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		return f
	}
	steps := func(raw json.RawMessage) fields {
		t.Helper()
		scenarios := decode(raw).Properties["scenarios"]
		stepList := decode(decode(scenarios).Items).Properties["steps"]
		return decode(decode(stepList).Items)
	}
	base, err := schemaFile.ReadFile("schema.json")
	if err != nil {
		t.Fatal(err)
	}
	actions := decode(steps(base).Properties["action"]).Enum
	found := map[string]bool{}
	for _, shape := range steps(setupSchema).AnyOf {
		for _, action := range decode(decode(shape).Properties["action"]).Enum {
			if found[action] && action != "fill" && action != "assert_json" {
				t.Fatalf("duplicate action %s", action)
			}
			found[action] = true
		}
	}
	for _, action := range actions {
		if !found[action] {
			t.Errorf("missing action %s", action)
		}
		delete(found, action)
	}
	if len(found) > 0 {
		t.Fatalf("unsupported actions: %v", found)
	}
}
