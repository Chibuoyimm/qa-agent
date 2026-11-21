package qa_test

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/Chibuoyimm/qa-agent/internal/qa"
)

func TestNamedJourneyApprovalScopeAndSetup(t *testing.T) {
	_, db := testStore(t)
	ctx := t.Context()
	const first = "http://localhost:4174"
	const second = "http://localhost:4175"
	store := qa.NewStore(db, map[string]bool{first: true, second: true})
	project, err := store.CreateProject(ctx, qa.ProjectInput{Name: "Trade", BaseURL: first})
	if err != nil {
		t.Fatal(err)
	}
	var seq int
	id := func() string { seq++; return fmt.Sprintf("%032x", seq) }
	task, err := store.CreateTask(ctx, project.ID, qa.TaskInput{RequestID: id(), Prompt: "Check trade", Title: "Trade", JourneyName: "All trade flows"})
	if err != nil {
		t.Fatal(err)
	}
	plan := func(p qa.Proposal) string {
		t.Helper()
		turn, _, err := store.BeginTaskChat(ctx, project.ID, task.ID, id(), "Prepare trade checks", "plan")
		if err != nil {
			t.Fatal(err)
		}
		if p.SavedFlowID != "" {
			p.FlowVersion = turn.ID
		}
		if _, err = store.FinishChat(ctx, project.ID, turn.ID, "Review", &p); err != nil {
			t.Fatal(err)
		}
		return turn.ID
	}
	draft := qa.ScenarioInput{Name: "Accept offer", ExpectedOutcome: "Trade terms locked", Steps: []qa.Step{{Action: "navigate", Path: "/"}, {Action: "assert_visible", TestID: "trade-terms"}}}
	sibling := qa.ScenarioInput{Name: "Withdraw offer", ExpectedOutcome: "Offer withdrawn", Steps: []qa.Step{{Action: "navigate", Path: "/"}, {Action: "assert_visible", TestID: "withdrawn"}}}
	source := plan(qa.Proposal{Scenarios: []qa.ScenarioInput{sibling, draft}})
	save := func(item string) string {
		t.Helper()
		result, err := store.SavePlanJourney(ctx, project.ID, task.ID, qa.SaveJourneyInput{RequestID: id(), Prompt: "Save a journey", TurnID: source, Item: item})
		if err != nil {
			t.Fatal(err)
		}
		return result.Turn.SavedJourneyID
	}
	flow := save("ready:1")
	save("ready:0")
	command := func() qa.RunJourneyInput {
		return qa.RunJourneyInput{RequestID: id(), Prompt: "run the offer acceptance journey", Reference: "offer acceptance", Mode: "advisory"}
	}
	run := func() qa.ChatTurn {
		t.Helper()
		r, e := store.RunSavedJourney(ctx, project.ID, task.ID, command(), nil)
		if e != nil || r.Turn == nil {
			t.Fatalf("resolve: %+v %v", r, e)
		}
		return *r.Turn
	}
	ambiguous := command()
	ambiguous.Reference = "offer"
	choices, err := store.RunSavedJourney(ctx, project.ID, task.ID, ambiguous, nil)
	if err != nil || len(choices.Choices) != 2 || choices.Turn != nil {
		t.Fatalf("ambiguous run %+v %v", choices, err)
	}
	foreign, err := store.CreateProject(ctx, qa.ProjectInput{Name: "Other project", BaseURL: first})
	if err != nil {
		t.Fatal(err)
	}
	foreignTask, err := store.CreateTask(ctx, foreign.ID, qa.TaskInput{RequestID: id(), Prompt: "Check trade", Title: "Trade", JourneyName: "Trade"})
	if err != nil {
		t.Fatal(err)
	}
	picked := command()
	picked.JourneyID = flow
	if _, err := store.RunSavedJourney(ctx, foreign.ID, foreignTask.ID, picked, nil); !errors.Is(err, qa.ErrNotFound) {
		t.Fatalf("foreign journey: %v", err)
	}
	approval, err := store.ApproveTask(ctx, project.ID, task.ID, qa.TaskApprovalInput{TurnID: source, BaseURL: second, Drafts: []qa.ReviewedDraft{{Index: 1, ScenarioInput: draft}, {Index: 0, ScenarioInput: sibling}}})
	if err != nil {
		t.Fatal(err)
	}
	review := run()
	if review.RunID != "" || review.Proposal == nil || len(review.Proposal.Scenarios) != 1 || review.Proposal.Scenarios[0].Name != draft.Name {
		t.Fatal("different target must show only the saved draft")
	}
	if _, err = db.Exec(ctx, "UPDATE projects SET base_url=$1 WHERE id=$2", second, project.ID); err != nil {
		t.Fatal(err)
	}
	// Two deliveries of one message can claim only one queue entry.
	in := command()
	var wg sync.WaitGroup
	results := make(chan qa.SaveJourneyResult, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := store.RunSavedJourney(ctx, project.ID, task.ID, in, nil)
			if e != nil {
				t.Error(e)
				return
			}
			results <- r
		}()
	}
	wg.Wait()
	close(results)
	var runID string
	for r := range results {
		if r.Turn == nil || r.Turn.RunID == "" || (runID != "" && runID != r.Turn.RunID) {
			t.Fatal("duplicate or missing named run")
		}
		runID = r.Turn.RunID
	}
	snapshot, err := store.GetRun(ctx, runID)
	if err != nil || snapshot.TaskID != task.ID || snapshot.BaseURL != second || len(snapshot.Scenarios) != 1 || snapshot.Scenarios[0].ID != approval.ScenarioIDs[0] {
		t.Fatalf("source approval scope: %+v %v", snapshot, err)
	}
	// Coverage keeps both the original check and its saved journey association.
	result := []qa.ScenarioResult{{ScenarioID: snapshot.Scenarios[0].ID, Status: "passed"}}
	if _, err := db.Exec(ctx, "UPDATE runs SET status='passed',finished_at=now(),results=$2 WHERE id=$1", runID, result); err != nil {
		t.Fatal(err)
	}
	coverage, err := store.CoverageForTarget(ctx, project.ID, second)
	if err != nil || len(coverage.Checks) != 1 || !slices.Equal(coverage.Checks[0].SavedJourneyIDs, []string{flow}) {
		t.Fatal("saved journey missing its result", err)
	}
	var count int
	if err = db.QueryRow(ctx, "SELECT count(*) FROM runs WHERE project_id=$1", project.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate run", count, err)
	}
	newer := draft
	newer.ExpectedOutcome = "Trade reference is present"
	newer.Steps = []qa.Step{{Action: "navigate", Path: "/"}, {Action: "assert_visible", TestID: "trade-reference"}}
	version := plan(qa.Proposal{SavedFlowID: flow, Scenarios: []qa.ScenarioInput{newer}})
	revised := run()
	if revised.RunID != "" || revised.Proposal == nil || revised.Proposal.FlowVersion != version || revised.Proposal.Scenarios[0].ExpectedOutcome != newer.ExpectedOutcome {
		t.Fatal("changed flow reused old approval")
	}
	nextApproval, err := store.ApproveTask(ctx, project.ID, task.ID, qa.TaskApprovalInput{TurnID: revised.ID, BaseURL: second, Drafts: []qa.ReviewedDraft{{Index: 0, ScenarioInput: newer}}})
	if err != nil {
		t.Fatal(err)
	}
	replay := run()
	snapshot, err = store.GetRun(ctx, replay.RunID)
	if err != nil || len(snapshot.Scenarios) != 1 || snapshot.Scenarios[0].JourneyID != flow || snapshot.Scenarios[0].ID != nextApproval.ScenarioIDs[0] {
		t.Fatal("reviewed flow did not use its own approved check", err)
	}
	missing := []string{"Provide the accepted counterparty."}
	plan(qa.Proposal{SavedFlowID: flow, Questions: missing})
	blocked := run()
	if blocked.RunID != "" || blocked.Proposal != nil || !slices.Equal(blocked.JourneySetup, missing) {
		t.Fatal("missing setup used an older approval")
	}
}
