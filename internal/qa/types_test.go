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

func TestJourneyBindingsAndCleanup(t *testing.T) {
	str := func(v string) *string { return &v }
	base := ScenarioInput{Name: "Trade journey", ExpectedOutcome: "A locked trade has the stated amount", Approved: true, Steps: []Step{
		{Action: "prepare_fixture", Value: str("escrow_local")}, {Action: "phase", Value: str("Create request")}, {Action: "use_actor", Value: str("requester")},
		{Action: "navigate", Path: "/new"}, {Action: "capture_id", Path: "/requests/", SaveAs: "request_id"},
		{Action: "assert_json_number", Path: "/api/requests/{{request_id}}", JSONPointer: "/amount", Value: str("1000")},
		{Action: "cleanup_fixture", Value: str("escrow_local")},
	}}
	if _, err := ValidateScenario(base); err != nil {
		t.Fatal(err)
	}
	cases := [][]Step{
		base.Steps[:len(base.Steps)-1],
		{{Action: "navigate", Path: "/requests/{{missing}}"}, {Action: "assert_visible", TestID: "ok"}},
		{{Action: "capture_json", Path: "/api/me", JSONPointer: "/access_token", SaveAs: "id"}, {Action: "assert_path", Path: "/"}},
		{{Action: "capture_id", Path: "/requests/", SaveAs: "run_id"}, {Action: "assert_path", Path: "/"}},
		{{Action: "capture_json", Path: "/api/me", JSONPointer: "/id", SaveAs: "amount"}, {Action: "assert_json_number", Path: "/api/data", JSONPointer: "/amount", ValueRef: "amount"}},
		{{Action: "assert_path", Path: "//evil.test"}},
	}
	for i, steps := range cases {
		candidate := base
		candidate.Steps = steps
		if _, err := ValidateScenario(candidate); err == nil {
			t.Fatalf("invalid journey %d accepted", i)
		}
	}
	scenario := Scenario{ID: "flow", ScenarioInput: base}
	result := ScenarioResult{ScenarioID: "flow", Status: "passed"}
	if _, err := ValidateResults([]Scenario{scenario}, []ScenarioResult{result}); err == nil {
		t.Fatal("accepted a journey pass without step evidence")
	}
	for i, step := range base.Steps {
		status := "passed"
		if i == 3 {
			status = "failed"
		}
		if i > 3 && i < len(base.Steps)-1 {
			status = "not_run"
		}
		result.StepResults = append(result.StepResults, ExecutionStepResult{Index: i, Action: step.Action, Status: status})
	}
	result.Status = "failed"
	if _, err := ValidateResults([]Scenario{scenario}, []ScenarioResult{result}); err != nil {
		t.Fatal("approved cleanup should execute after failure", err)
	}
	result.StepResults[4].Status = "passed"
	if _, err := ValidateResults([]Scenario{scenario}, []ScenarioResult{result}); err == nil {
		t.Fatal("accepted dependent activity after a failure")
	}
}
