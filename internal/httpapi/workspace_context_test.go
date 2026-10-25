package httpapi

import (
	"strings"
	"testing"

	"github.com/Chibuoyimm/qa-agent/internal/qa"
)

func TestDiscoverySharesKnownAccessWithoutLiteralCredentials(t *testing.T) {
	literal := "private-literal-login"
	secret := "QA_TEST_PASSWORD"
	discovery := &qa.Discovery{BaseURL: "https://app.example", StartPath: "/", Result: &qa.DiscoveryResult{}, SetupScenario: &qa.Scenario{ScenarioInput: qa.ScenarioInput{Steps: []qa.Step{{Action: "navigate", Path: "/known-login"}, {Action: "fill", TestID: "email", Value: &literal}, {Action: "fill", TestID: "password", SecretEnv: &secret}, {Action: "click", TestID: "submit"}}}}}
	context, err := assembleProposalContext("Independent requirement", nil, discovery)
	if err != nil {
		t.Fatal(err)
	}
	for _, known := range []string{"/known-login", "QA_TEST_PASSWORD", "approved_access_steps", "Independent requirement"} {
		if !strings.Contains(context, known) {
			t.Fatal("missing known access", known)
		}
	}
	if strings.Contains(context, literal) {
		t.Fatal("literal credential leaked")
	}
	if discovery.SetupScenario.Steps[1].Value == nil {
		t.Fatal("context assembly modified saved access definition")
	}
}

func TestRunDiscussionSharesOutcomesWithoutActionCredentials(t *testing.T) {
	literal := "private-fill-value"
	expected := "140000"
	run := qa.Run{BaseURL: "https://preview.example", Status: "failed", Scenarios: []qa.Scenario{{ID: "one", ScenarioInput: qa.ScenarioInput{Name: "Revenue", ExpectedOutcome: "Revenue is 140000", Steps: []qa.Step{{Action: "fill", Value: &literal}, {Action: "assert_text", TestID: "revenue", Value: &expected}}}}}, Results: []qa.ScenarioResult{{ScenarioID: "one", Status: "failed", Message: "Wrong revenue", StepResults: []qa.ExecutionStepResult{{Index: 0, Action: "fill", Status: "passed"}, {Index: 1, Action: "assert_text", Status: "failed", Observed: "230000"}}, Artifacts: []qa.Artifact{{Kind: "screenshot", Path: "private-media.png"}}}}}
	context, err := runContext("Facts", run)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(context, "Revenue is 140000") || !strings.Contains(context, "Wrong revenue") || !strings.Contains(context, "230000") {
		t.Fatal("outcome omitted")
	}
	if strings.Contains(context, literal) || strings.Contains(context, "private-media") {
		t.Fatal("unnecessary private context shared")
	}
	if _, err := runContext(strings.Repeat("x", 48000), run); err == nil {
		t.Fatal("accepted oversized discussion context")
	}
}

func TestJourneyFollowUpKeepsTheFullBusinessExpectations(t *testing.T) {
	stage := "Publish and accept"
	steps := []qa.Step{{Action: "phase", Value: &stage}}
	for i := 0; i < 100; i++ {
		amount := "1555000.00000000"
		steps = append(steps, qa.Step{Action: "assert_json_number", Path: "/api/trades/{{trade_id}}", JSONPointer: "/to_amount", Value: &amount})
	}
	turn := qa.ChatTurn{Status: "completed", Prompt: "Test the complete trade journey", Proposal: &qa.Proposal{Scenarios: []qa.ScenarioInput{{Name: "Trade", Steps: steps}}}}
	context, err := chatContext("Source evidence", []qa.ChatTurn{turn})
	if err != nil || strings.Count(context, "1555000.00000000") != 100 || !strings.Contains(context, turn.Prompt) {
		t.Fatalf("follow-up lost the complete journey: %v", err)
	}
}
