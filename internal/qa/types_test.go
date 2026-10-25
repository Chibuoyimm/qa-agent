package qa

import "testing"

func TestScenarioValidation(t *testing.T) {
	value := "total"
	tests := []struct {
		name  string
		steps []Step
	}{
		{"no assertion", []Step{{Action: "click", TestID: "button"}}},
		{"cross origin path", []Step{{Action: "navigate", Path: "//evil.test"}, {Action: "assert_visible", TestID: "total"}}},
		{"fill both values", []Step{{Action: "fill", TestID: "email", Value: &value, SecretEnv: &value}, {Action: "assert_visible", TestID: "total"}}},
		{"unsupported action", []Step{{Action: "evaluate", TestID: "total"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateScenario(ScenarioInput{Name: "check", ExpectedOutcome: "total visible", Approved: true, Steps: tt.steps})
			if err == nil {
				t.Fatal("expected invalid scenario")
			}
		})
	}
}

func TestResultPrecedenceAndArtifacts(t *testing.T) {
	scenarios := []Scenario{{ID: "one"}, {ID: "two"}}
	results := []ScenarioResult{{ScenarioID: "one", Status: "failed"}, {ScenarioID: "two", Status: "blocked"}}
	status, err := ValidateResults(scenarios, results)
	if err != nil || status != "blocked" {
		t.Fatalf("blocked precedence: %q %v", status, err)
	}
	results[1].Status = "error"
	status, err = ValidateResults(scenarios, results)
	if err != nil || status != "error" {
		t.Fatalf("error precedence: %q %v", status, err)
	}
	results[1].Artifacts = []Artifact{{Kind: "screenshot", Path: "../secret.png"}}
	if _, err := ValidateResults(scenarios, results); err == nil {
		t.Fatal("accepted traversal artifact")
	}
}

func TestExecutionStepRecordsCannotClaimUnreachedSteps(t *testing.T) {
	scenarios := []Scenario{{ID: "check", ScenarioInput: ScenarioInput{Steps: []Step{{Action: "navigate"}, {Action: "assert_text"}}}}}
	result := ScenarioResult{ScenarioID: "check", Status: "failed", StepResults: []ExecutionStepResult{{Index: 0, Action: "navigate", Status: "passed"}, {Index: 1, Action: "assert_text", Status: "failed", Observed: "wrong total"}}}
	if _, err := ValidateResults(scenarios, []ScenarioResult{result}); err != nil {
		t.Fatal(err)
	}
	result.StepResults[0].Status = "failed"
	if _, err := ValidateResults(scenarios, []ScenarioResult{result}); err == nil {
		t.Fatal("accepted execution after a failed step")
	}
	result.StepResults = result.StepResults[:1]
	result.StepResults[0].Status = "passed"
	result.Status = "passed"
	if _, err := ValidateResults(scenarios, []ScenarioResult{result}); err == nil {
		t.Fatal("accepted a pass with unreached assertions")
	}
	result.StepResults = nil
	if _, err := ValidateResults(scenarios, []ScenarioResult{result}); err != nil {
		t.Fatal("legacy worker compatibility", err)
	}
}

func TestAccessibleTargets(t *testing.T) {
	value := "demo@example.test"
	for _, target := range []ElementLocator{{Label: "Email address"}, {Role: "button", Name: "Sign in"}, {Role: "heading", Name: "Créez votre compte"}} {
		_, err := ValidateScenario(ScenarioInput{Name: "check", ExpectedOutcome: "visible", Approved: true, Steps: []Step{{Action: "fill", Locator: &target, Value: &value}, {Action: "assert_visible", Locator: &target}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, step := range []Step{
		{Action: "click", Locator: &ElementLocator{}},
		{Action: "click", TestID: "button", Locator: &ElementLocator{Role: "button", Name: "Sign in"}},
		{Action: "click", Locator: &ElementLocator{Label: "Email", Role: "textbox", Name: "Email"}},
		{Action: "click", Locator: &ElementLocator{Role: "css", Name: "#submit"}},
		{Action: "navigate", Path: "/", Locator: &ElementLocator{Label: "Email"}},
	} {
		if err := validateStep(step); err == nil {
			t.Fatalf("accepted invalid target: %+v", step)
		}
	}
	original := []Step{{Action: "assert_visible", Locator: &ElementLocator{Role: "heading", Name: "Denied"}}}
	changed := []Step{{Action: "assert_visible", Locator: &ElementLocator{Role: "heading", Name: "Allowed"}}}
	if assertionKeys(original)[0] == assertionKeys(changed)[0] {
		t.Fatal("assertion identity ignored accessible target")
	}
}
