package qa_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Chibuoyimm/qa-agent/internal/qa"
)

func TestWorkspaceApprovalCoverageAndFrozenRecheck(t *testing.T) {
	store, db := testStore(t)
	ctx := context.Background()
	store = qa.NewStore(db, map[string]bool{"http://localhost:4174": true, "http://localhost:4175": true})
	project, err := store.CreateProject(ctx, qa.ProjectInput{Name: "Workspace flow", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(ctx, project.ID, qa.TaskInput{RequestID: strings.Repeat("a", 32), Prompt: "Check report access", Title: "Report access", JourneyName: "Report access"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateTask(ctx, project.ID, qa.TaskInput{RequestID: strings.Repeat("b", 32), Prompt: "Check sign in", Title: "Sign in", JourneyName: "Sign in"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTask(ctx, project.ID, qa.TaskInput{RequestID: task.ID, Prompt: task.Request, Title: task.Title, JourneyName: "Report access"}); err != nil {
		t.Fatal("idempotent task", err)
	}
	first, _, err := store.BeginTaskChat(ctx, project.ID, task.ID, strings.Repeat("c", 32), task.Request, "first")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := store.BeginTaskChat(ctx, project.ID, other.ID, strings.Repeat("d", 32), other.Request, "second")
	if err != nil {
		t.Fatal("other task blocked by pending chat", err)
	}
	draft := qa.ScenarioInput{Name: "Report hidden", ExpectedOutcome: "The removed account cannot see the report", Steps: []qa.Step{{Action: "navigate", Path: "/report"}, {Action: "assert_visible", TestID: "access-denied"}}}
	if _, err := store.FinishChat(ctx, project.ID, first.ID, "Review this check", &qa.Proposal{Scenarios: []qa.ScenarioInput{draft}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishChat(ctx, project.ID, second.ID, "Need the sign-in requirement", &qa.Proposal{Questions: []string{"What should happen?"}}); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListTaskChat(ctx, project.ID, task.ID, 0)
	if err != nil || len(page.Turns) != 1 || page.Turns[0].ID != first.ID {
		t.Fatalf("task history leaked: %+v %v", page, err)
	}
	input := qa.TaskApprovalInput{TurnID: first.ID, Drafts: []qa.ReviewedDraft{{Index: 0, ScenarioInput: draft}}}
	approval, err := store.ApproveTask(ctx, project.ID, task.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := store.ApproveTask(ctx, project.ID, task.ID, input)
	if err != nil || !reflect.DeepEqual(approval, repeated) {
		t.Fatal("approval duplicated", err)
	}
	if _, err := store.ApproveTask(ctx, project.ID, other.ID, input); !errors.Is(err, qa.ErrNotFound) {
		t.Fatalf("cross-task proposal allowed: %v", err)
	}
	scenarios, err := store.ListScenarios(ctx, project.ID)
	if err != nil || len(scenarios) != 1 || scenarios[0].JourneyID != task.JourneyID {
		t.Fatalf("duplicate or ungrouped checks: %+v %v", scenarios, err)
	}
	turn, err := store.CreateChatRun(ctx, project.ID, qa.ChatRunInput{TaskID: task.ID, RequestID: strings.Repeat("e", 32), Prompt: "Run approved checks", Action: "selected", ScenarioIDs: approval.ScenarioIDs, Mode: "advisory"}, "run")
	if err != nil {
		t.Fatal(err)
	}
	run, lease, _, found, err := store.ClaimRun(ctx, "workspace-worker")
	if err != nil || !found || run.ID != turn.RunID || run.TaskID != task.ID {
		t.Fatalf("task run: %+v %v", run, err)
	}
	result := qa.ScenarioResult{ScenarioID: approval.ScenarioIDs[0], Status: "failed", Message: "Report was visible", Artifacts: []qa.Artifact{}}
	if _, err := store.CompleteRun(ctx, run.ID, qa.CompleteInput{LeaseToken: lease, Results: []qa.ScenarioResult{result}}); err != nil {
		t.Fatal(err)
	}
	review, err := store.ReviewFinding(ctx, run.ID, qa.FindingReview{ScenarioID: result.ScenarioID, Status: "Confirmed issue", Owner: "Engineering"})
	if err != nil || review.UpdatedAt.IsZero() {
		t.Fatal(err)
	}
	if _, err := store.ReviewFinding(ctx, run.ID, qa.FindingReview{ScenarioID: result.ScenarioID, Status: "Works as intended"}); !errors.Is(err, qa.ErrInvalid) {
		t.Fatal("missing reason accepted")
	}
	// A later edit cannot alter the expectations used by the recheck.
	if _, err := db.Exec(ctx, `UPDATE scenarios SET expected_outcome='A different new expectation' WHERE id=$1`, result.ScenarioID); err != nil {
		t.Fatal(err)
	}
	recheckInput := qa.RecheckInput{RequestID: strings.Repeat("f", 32), BaseURL: "http://localhost:4175", Mode: "advisory"}
	recheck, err := store.RecheckRun(ctx, run.ID, recheckInput)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recheck.Scenarios, run.Scenarios) || recheck.TaskID != task.ID || recheck.SourceRunID != run.ID || recheck.BaseURL != "http://localhost:4175" {
		t.Fatalf("recheck changed proof: %+v", recheck)
	}
	replay, err := store.RecheckRun(ctx, run.ID, recheckInput)
	if err != nil || replay.ID != recheck.ID {
		t.Fatal("duplicate recheck", err)
	}
	if _, err := store.RecheckRun(ctx, run.ID, qa.RecheckInput{RequestID: strings.Repeat("1", 32), BaseURL: "http://unapproved.test", Mode: "advisory"}); !errors.Is(err, qa.ErrInvalid) {
		t.Fatal("unapproved target accepted", err)
	}
	claimed, lease, _, found, err := store.ClaimRun(ctx, "workspace-worker")
	if err != nil || !found || claimed.ID != recheck.ID {
		t.Fatal("recheck claim", err)
	}
	result.Status = "passed"
	result.Message = "Access denied"
	if _, err := store.CompleteRun(ctx, recheck.ID, qa.CompleteInput{LeaseToken: lease, Results: []qa.ScenarioResult{result}}); err != nil {
		t.Fatal(err)
	}
	coverage, err := store.Coverage(ctx, project.ID)
	if err != nil || len(coverage.Journeys) != 2 || len(coverage.Checks) != 1 || coverage.Checks[0].Result.Status != "passed" || coverage.Checks[0].RunID != recheck.ID {
		t.Fatalf("coverage double-counted or stale: %+v %v", coverage, err)
	}
	original, err := store.GetRun(ctx, run.ID)
	if err != nil || original.Status != "failed" || original.Scenarios[0].ExpectedOutcome != draft.ExpectedOutcome {
		t.Fatal("original evidence lost", err)
	}
}

func TestTaskDiscoveryScopeAndRetry(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	project, err := store.CreateProject(ctx, qa.ProjectInput{Name: "Discovery flow", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(ctx, project.ID, qa.TaskInput{RequestID: strings.Repeat("a", 32), Title: "Explore", Prompt: "Explore login", JourneyName: "Login"})
	if err != nil {
		t.Fatal(err)
	}
	input := qa.TaskDiscoveryInput{DiscoveryInput: qa.DiscoveryInput{StartPath: "/", MaxPages: 3}}
	discovery, err := store.DiscoverTask(ctx, project.ID, task.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := store.DiscoverTask(ctx, project.ID, task.ID, input)
	if err != nil || discovery.ID != repeated.ID {
		t.Fatal("retry created duplicate discovery", err)
	}
	saved, err := store.GetTask(ctx, project.ID, task.ID)
	if err != nil || saved.DiscoveryID != discovery.ID {
		t.Fatal("discovery not attached", err)
	}
	other, err := store.CreateProject(ctx, qa.ProjectInput{Name: "Other", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DiscoverTask(ctx, other.ID, task.ID, input); !errors.Is(err, qa.ErrNotFound) {
		t.Fatal("cross project task accepted", err)
	}
	if _, err := store.CreateTask(ctx, other.ID, qa.TaskInput{RequestID: strings.Repeat("b", 32), Title: "Other", Prompt: "Other journey", JourneyID: task.JourneyID}); !errors.Is(err, qa.ErrInvalid) {
		t.Fatal("cross-project journey accepted", err)
	}
}

func TestOutcomeEditCannotApproveUnchangedAssertions(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	project, err := store.CreateProject(ctx, qa.ProjectInput{Name: "Approval regression", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(ctx, project.ID, qa.TaskInput{RequestID: strings.Repeat("a", 32), Prompt: "Check sign in", Title: "Sign in", JourneyName: "Sign in"})
	if err != nil {
		t.Fatal(err)
	}
	turn, _, err := store.BeginTaskChat(ctx, project.ID, task.ID, strings.Repeat("b", 32), "Check sign in", "request")
	if err != nil {
		t.Fatal(err)
	}
	value := "Sign in"
	draft := qa.ScenarioInput{Name: "Sign in", ExpectedOutcome: "The button says Sign in", Steps: []qa.Step{{Action: "navigate", Path: "/"}, {Action: "assert_text", TestID: "submit", Value: &value}}}
	if _, err = store.FinishChat(ctx, project.ID, turn.ID, "Review", &qa.Proposal{Scenarios: []qa.ScenarioInput{draft}}); err != nil {
		t.Fatal(err)
	}
	draft.ExpectedOutcome = "The button says Continue"
	input := qa.TaskApprovalInput{TurnID: turn.ID, Drafts: []qa.ReviewedDraft{{Index: 0, ScenarioInput: draft}}}
	if _, err = store.ApproveTask(ctx, project.ID, task.ID, input); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("prose-only edit accepted: %v", err)
	}
	draft.Steps[0].Path = "/login"
	if _, err = store.ApproveTask(ctx, project.ID, task.ID, input); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("navigation edit bypassed guard: %v", err)
	}
	checks, err := store.ListScenarios(ctx, project.ID)
	if err != nil || len(checks) != 0 {
		t.Fatalf("rejected approval saved checks: %v", err)
	}
	revised := "Continue"
	draft.Steps[1].Value = &revised
	if _, err = store.ApproveTask(ctx, project.ID, task.ID, input); err != nil {
		t.Fatal(err)
	}
}

func TestVerifiedAccessRequiresSuccessfulScopedDiscovery(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	project, err := store.CreateProject(ctx, qa.ProjectInput{Name: "Access", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	setup := approvedScenario(t, store, project.ID)
	discovery, err := store.CreateDiscovery(ctx, project.ID, qa.DiscoveryInput{StartPath: "/", MaxPages: 1, SetupScenarioID: setup.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SaveVerifiedAccess(ctx, project.ID, discovery.ID); !errors.Is(err, qa.ErrInvalid) {
		t.Fatal("unverified access accepted", err)
	}
	_, lease, _, found, err := store.ClaimDiscovery(ctx, "worker")
	if err != nil || !found {
		t.Fatal(err)
	}
	if _, err = store.CompleteDiscovery(ctx, discovery.ID, qa.CompleteDiscoveryInput{LeaseToken: lease, Result: discoveryResult()}); err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateProject(ctx, qa.ProjectInput{Name: "Other", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SaveVerifiedAccess(ctx, other.ID, discovery.ID); !errors.Is(err, qa.ErrInvalid) {
		t.Fatal("foreign access accepted", err)
	}
	saved, err := store.SaveVerifiedAccess(ctx, project.ID, discovery.ID)
	if err != nil || saved.SetupScenarioID != setup.ID || saved.VerifiedAt == nil {
		t.Fatalf("default setup missing: %+v %v", saved, err)
	}
	loaded, err := store.GetVerifiedAccess(ctx, project.ID)
	if err != nil || loaded.DiscoveryID != discovery.ID {
		t.Fatal("default not persisted", err)
	}
}
