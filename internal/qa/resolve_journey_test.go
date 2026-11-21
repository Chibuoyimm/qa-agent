package qa_test

import (
	"errors"
	"testing"

	"github.com/Chibuoyimm/qa-agent/internal/qa"
)

func TestResolvedRunScopeAndReplay(t *testing.T) {
	store, db := testStore(t)
	ctx := t.Context()
	project, err := store.CreateProject(ctx, qa.ProjectInput{Name: "Shop", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(ctx, project.ID, qa.TaskInput{RequestID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1", Title: "Test checkout", Prompt: "Pay for a cart", JourneyName: "Checkout"})
	if err != nil {
		t.Fatal(err)
	}
	journey, question, err := store.SetupJourney(ctx, project.ID, task, "", "checkout")
	if err != nil || question != "" || journey != task.JourneyID {
		t.Fatalf("journey %q %q %v", journey, question, err)
	}
	_, question, err = store.SetupJourney(ctx, project.ID, task, "", "refund")
	if err != nil || question == "" {
		t.Fatalf("unknown journey %q %v", question, err)
	}
	turn, _, err := store.BeginTaskChat(ctx, project.ID, task.ID, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa2", "Resolve setup and run checkout", "resolve:1")
	if err != nil {
		t.Fatal(err)
	}
	draft := qa.ScenarioInput{Name: "Checkout", ExpectedOutcome: "Cart total is visible", Steps: []qa.Step{{Action: "navigate", Path: "/"}, {Action: "assert_visible", TestID: "cart-total"}}}
	p := qa.Proposal{SavedFlowID: journey, FlowVersion: turn.ID, Scenarios: []qa.ScenarioInput{draft}}
	p.Questions = []string{"Which staging target?"}
	if _, err := store.FinishResolvedRun(ctx, project.ID, task.ID, turn.ID, project.BaseURL, p); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("queued despite missing setup: %v", err)
	}
	p.Questions = nil
	if _, err := store.FinishResolvedRun(ctx, project.ID, task.ID, turn.ID, "http://localhost:9999", p); !errors.Is(err, qa.ErrConflict) {
		t.Fatalf("target changed: %v", err)
	}
	got, err := store.FinishResolvedRun(ctx, project.ID, task.ID, turn.ID, project.BaseURL, p)
	if err != nil || got.RunID == "" {
		t.Fatalf("queue: %v", err)
	}
	again, fresh, err := store.BeginTaskChat(ctx, project.ID, task.ID, turn.ID, turn.Prompt, "resolve:1")
	if err != nil || fresh || again.RunID != got.RunID {
		t.Fatalf("replay %+v %v", again, err)
	}
	var count int
	if err = db.QueryRow(ctx, "SELECT count(*) FROM runs WHERE project_id=$1", project.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("queued %d: %v", count, err)
	}
	named, err := store.RunSavedJourney(ctx, project.ID, task.ID, qa.RunJourneyInput{RequestID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa3", Prompt: "Run the checkout journey", Reference: "checkout", Mode: "advisory"}, nil)
	if err != nil || named.Turn == nil || named.Turn.RunID == "" {
		t.Fatalf("reuse fresh resolved journey: %+v %v", named, err)
	}
	recheck, err := store.GetRun(ctx, named.Turn.RunID)
	if err != nil || len(recheck.Scenarios) != 1 || recheck.Scenarios[0].ExpectedOutcome != draft.ExpectedOutcome {
		t.Fatalf("recheck snapshot: %+v %v", recheck, err)
	}

}
