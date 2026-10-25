package qa_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Chibuoyimm/qa-agent/internal/qa"
)

func TestJourneyProgressRetainsCancellationAndReportsCleanup(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	project, err := store.CreateProject(ctx, qa.ProjectInput{Name: "Journey project", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	str := func(s string) *string { return &s }
	scenario, err := store.CreateScenario(ctx, project.ID, qa.ScenarioInput{Name: "Journey", ExpectedOutcome: "Private page is visible", Approved: true, Steps: []qa.Step{{Action: "prepare_fixture", Value: str("local_test")}, {Action: "phase", Value: str("Verify access")}, {Action: "assert_path", Path: "/private"}, {Action: "cleanup_fixture", Value: str("local_test")}}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(ctx, project.ID, qa.RunInput{ScenarioIDs: []string{scenario.ID}, Mode: "blocking"})
	if err != nil {
		t.Fatal(err)
	}
	_, lease, _, found, err := store.ClaimRun(ctx, "journey-worker")
	if err != nil || !found {
		t.Fatal(err)
	}
	result := qa.ScenarioResult{ScenarioID: scenario.ID, Status: "running", Message: "Preparing", StepResults: []qa.ExecutionStepResult{{Index: 0, Action: "prepare_fixture", Status: "passed"}}}
	if err := store.RecordProgress(ctx, run.ID, qa.CompleteInput{LeaseToken: lease, Results: []qa.ScenarioResult{result}}); err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetRun(ctx, run.ID)
	if err != nil || saved.Status != "running" || len(saved.Results) != 1 {
		t.Fatalf("progress missing: %+v %v", saved, err)
	}
	if _, err := store.CancelRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	result.Status = "error"
	result.StepResults = append(result.StepResults, qa.ExecutionStepResult{Index: 1, Action: "phase", Status: "error"}, qa.ExecutionStepResult{Index: 2, Action: "assert_path", Status: "not_run"}, qa.ExecutionStepResult{Index: 3, Action: "cleanup_fixture", Status: "passed"})
	input := qa.CompleteInput{LeaseToken: lease, Results: []qa.ScenarioResult{result}}
	if err := store.RecordProgress(ctx, run.ID, input); err != nil {
		t.Fatal("cancelled worker must report cleanup", err)
	}
	if _, err := store.CompleteRun(ctx, run.ID, input); !errors.Is(err, qa.ErrConflict) {
		t.Fatal("late completion changed cancellation", err)
	}
	input.LeaseToken = "wrong"
	if err := store.RecordProgress(ctx, run.ID, input); !errors.Is(err, qa.ErrConflict) {
		t.Fatal("foreign lease changed progress", err)
	}
	saved, err = store.GetRun(ctx, run.ID)
	if err != nil || saved.Status != "cancelled" || saved.Results[0].StepResults[3].Status != "passed" {
		t.Fatalf("cleanup or cancellation lost: %+v %v", saved, err)
	}
}
