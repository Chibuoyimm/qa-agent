package qa_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/Chibuoyimm/qa-agent/internal/qa"
)

func TestChatRunApprovalScopeAndIdempotency(t *testing.T) {
	store, db := testStore(t)
	ctx := context.Background()
	project, err := store.CreateProject(ctx, qa.ProjectInput{Name: "chat", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	approved := approvedScenario(t, store, project.ID)
	draft, err := store.CreateScenario(ctx, project.ID, qa.ScenarioInput{Name: "unapproved", ExpectedOutcome: "Required", Steps: []qa.Step{{Action: "navigate", Path: "/"}, {Action: "assert_visible", TestID: "fixture"}}})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateProject(ctx, qa.ProjectInput{Name: "other", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	foreign := approvedScenario(t, store, other.ID)
	in := qa.ChatRunInput{RequestID: "11111111111111111111111111111111", Prompt: "Run all approved checks", Action: "all_approved", Mode: "blocking"}
	var wg sync.WaitGroup
	turns := make(chan qa.ChatTurn, 8)
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			turn, err := store.CreateChatRun(ctx, project.ID, in, "same-input")
			if err != nil {
				failures <- err
			} else {
				turns <- turn
			}
		}()
	}
	wg.Wait()
	close(turns)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var id string
	for turn := range turns {
		if id != "" && turn.RunID != id {
			t.Fatal("duplicate chat request created another run")
		}
		id = turn.RunID
	}
	run, err := store.GetRun(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Scenarios) != 1 || run.Scenarios[0].ID != approved.ID || run.Gate != "pending" {
		t.Fatalf("wrong frozen run: %+v", run)
	}
	if _, err := store.CreateChatRun(ctx, project.ID, in, "changed-input"); !errors.Is(err, qa.ErrConflict) {
		t.Fatalf("changed replay: %v", err)
	}
	for i, ids := range [][]string{{draft.ID}, {foreign.ID}} {
		in.RequestID = fmt.Sprintf("%032x", i+20)
		in.Action = "selected"
		in.ScenarioIDs = ids
		if _, err := store.CreateChatRun(ctx, project.ID, in, "invalid"); err == nil {
			t.Fatal("ran unapproved or foreign scenario")
		}
	}
	if _, err := db.Exec(ctx, `UPDATE runs SET status='failed',gate='fail',results=$2 WHERE id=$1`, id, `[{"scenario_id":"`+approved.ID+`","status":"failed","message":"wrong total","duration_ms":1,"artifacts":[]}]`); err != nil {
		t.Fatal(err)
	}
	in = qa.ChatRunInput{RequestID: "22222222222222222222222222222222", Prompt: "Rerun failed checks", Action: "rerun_failed", Mode: "advisory", SourceRunID: id}
	retry, err := store.CreateChatRun(ctx, project.ID, in, "retry")
	if err != nil {
		t.Fatal(err)
	}
	rerun, err := store.GetRun(ctx, retry.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rerun.Scenarios) != 1 || rerun.Scenarios[0].ExpectedOutcome != run.Scenarios[0].ExpectedOutcome {
		t.Fatal("rerun changed frozen expectation")
	}
	in.RequestID = "33333333333333333333333333333333"
	if _, err := store.CreateChatRun(ctx, other.ID, in, "foreign-run"); !errors.Is(err, qa.ErrNotFound) {
		t.Fatalf("foreign source run: %v", err)
	}
	runs, err := store.ListRuns(ctx, project.ID)
	if err != nil || len(runs) != 2 {
		t.Fatalf("rejected requests created runs: %d %v", len(runs), err)
	}
}

func TestChatPendingRecoveryAndHistoryPaging(t *testing.T) {
	store, db := testStore(t)
	ctx := context.Background()
	project, err := store.CreateProject(ctx, qa.ProjectInput{Name: "history", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	turn, fresh, err := store.BeginChat(ctx, project.ID, "11111111111111111111111111111111", "Test login", "request")
	if err != nil || !fresh {
		t.Fatalf("begin %v %v", fresh, err)
	}
	if _, _, err := store.BeginChat(ctx, project.ID, "22222222222222222222222222222222", "Another request", "another"); !errors.Is(err, qa.ErrConflict) {
		t.Fatalf("parallel drafting not rejected: %v", err)
	}
	if _, err := db.Exec(ctx, `UPDATE chat_turns SET created_at=clock_timestamp()-interval '101 seconds' WHERE id=$1`, turn.ID); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListChat(ctx, project.ID, 0)
	if err != nil || page.Turns[0].Status != "error" {
		t.Fatalf("interrupted recovery: %+v %v", page, err)
	}
	if _, err := store.FinishChat(ctx, project.ID, turn.ID, "late", &qa.Proposal{}); !errors.Is(err, qa.ErrConflict) {
		t.Fatalf("late inference overwrote interrupted request: %v", err)
	}
	for i := 2; i < 24; i++ {
		id := fmt.Sprintf("%032x", i)
		if _, _, err := store.BeginChat(ctx, project.ID, id, "Question", "hash"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.FinishChat(ctx, project.ID, id, "Need expected value", &qa.Proposal{Provider: "openai", Model: "fixture", Questions: []string{"What is the expected value?"}, Scenarios: []qa.ScenarioInput{}, Assumptions: []string{}}); err != nil {
			t.Fatal(err)
		}
	}
	page, err = store.ListChat(ctx, project.ID, 0)
	if err != nil || len(page.Turns) != 20 || !page.HasOlder {
		t.Fatalf("page %+v %v", page, err)
	}
	older, err := store.ListChat(ctx, project.ID, page.Turns[0].Sequence)
	if err != nil || len(older.Turns) != 3 || older.HasOlder {
		t.Fatalf("older %+v %v", older, err)
	}
	if older.Turns[len(older.Turns)-1].Sequence >= page.Turns[0].Sequence {
		t.Fatal("overlapping or unordered page")
	}
}
