package qa_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/Chibuoyimm/qa-agent/internal/qa"
	"github.com/Chibuoyimm/qa-agent/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testStore(t *testing.T) (*qa.Store, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	db, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	schema := "qa_test_" + hex.EncodeToString(suffix[:])
	if _, err := db.Exec(context.Background(), `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	testDB, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		testDB.Close()
		if _, err := db.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
		db.Close()
	})
	if err := migrations.Apply(context.Background(), testDB); err != nil {
		t.Fatal(err)
	}
	return qa.NewStore(testDB, map[string]bool{"http://localhost:4174": true}), testDB
}

func cleanupProject(t *testing.T, db *pgxpool.Pool, id string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, query := range []string{
			`DELETE FROM runs WHERE project_id=$1`,
			`DELETE FROM scenarios WHERE project_id=$1`,
			`DELETE FROM projects WHERE id=$1`,
		} {
			if _, err := db.Exec(ctx, query, id); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		}
	})
}

func approvedScenario(t *testing.T, store *qa.Store, projectID string) qa.Scenario {
	t.Helper()
	expected := "140000"
	scenario, err := store.CreateScenario(context.Background(), projectID, qa.ScenarioInput{
		Name: "Revenue total", ExpectedOutcome: "Net revenue is 140000", Approved: true,
		Steps: []qa.Step{{Action: "navigate", Path: "/dashboard"}, {Action: "assert_text", TestID: "revenue", Value: &expected}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return scenario
}

func TestRunLeaseAndPersistence(t *testing.T) {
	store, db := testStore(t)
	ctx := context.Background()
	project, err := store.CreateProject(ctx, qa.ProjectInput{Name: "pilot", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	cleanupProject(t, db, project.ID)
	scenario := approvedScenario(t, store, project.ID)
	unapproved, err := store.CreateScenario(ctx, project.ID, qa.ScenarioInput{
		Name: "draft", ExpectedOutcome: "draft outcome", Approved: false,
		Steps: []qa.Step{{Action: "assert_visible", TestID: "draft"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRun(ctx, project.ID, qa.RunInput{ScenarioIDs: []string{unapproved.ID}, Mode: "blocking"}); !errors.Is(err, qa.ErrConflict) {
		t.Fatalf("unapproved scenario: %v", err)
	}
	if _, err := store.CreateRun(ctx, project.ID, qa.RunInput{ScenarioIDs: []string{scenario.ID, scenario.ID}, Mode: "blocking"}); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("duplicate scenario: %v", err)
	}
	run, err := store.CreateRun(ctx, project.ID, qa.RunInput{ScenarioIDs: []string{scenario.ID}, Mode: "blocking"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "queued" || run.Gate != "pending" || len(run.Scenarios) != 1 {
		t.Fatalf("queued run: %+v", run)
	}
	claimed, token, _, found, err := store.ClaimRun(ctx, "worker-one")
	if err != nil || !found || claimed.ID != run.ID || token == "" {
		t.Fatalf("claim: %+v %q %v %v", claimed, token, found, err)
	}
	if _, err := store.Heartbeat(ctx, run.ID, "wrong"); !errors.Is(err, qa.ErrConflict) {
		t.Fatalf("wrong heartbeat: %v", err)
	}
	if _, err := store.Heartbeat(ctx, run.ID, token); err != nil {
		t.Fatal(err)
	}
	result := qa.ScenarioResult{ScenarioID: scenario.ID, Status: "failed", Message: "expected 140000, saw 230000", DurationMS: 12, Artifacts: []qa.Artifact{}}
	if _, err := store.CompleteRun(ctx, run.ID, qa.CompleteInput{LeaseToken: token, Results: []qa.ScenarioResult{}}); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("incomplete results: %v", err)
	}
	if _, err := store.CompleteRun(ctx, run.ID, qa.CompleteInput{LeaseToken: token, Results: []qa.ScenarioResult{result, result}}); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("duplicate results: %v", err)
	}
	completed, err := store.CompleteRun(ctx, run.ID, qa.CompleteInput{LeaseToken: token, Results: []qa.ScenarioResult{result}})
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "failed" || completed.Gate != "fail" || completed.FinishedAt == nil {
		t.Fatalf("completed: %+v", completed)
	}
	if _, err := store.CompleteRun(ctx, run.ID, qa.CompleteInput{LeaseToken: token, Results: []qa.ScenarioResult{result}}); !errors.Is(err, qa.ErrConflict) {
		t.Fatalf("late completion: %v", err)
	}
	restarted := qa.NewStore(db, map[string]bool{"http://localhost:4174": true})
	persisted, err := restarted.GetRun(ctx, run.ID)
	if err != nil || persisted.Status != "failed" || persisted.Results[0].Message != result.Message || persisted.Scenarios[0].ExpectedOutcome != scenario.ExpectedOutcome {
		t.Fatalf("persisted result: %+v %v", persisted, err)
	}
}

func TestCancellationAndExpiredLease(t *testing.T) {
	store, db := testStore(t)
	ctx := context.Background()
	project, err := store.CreateProject(ctx, qa.ProjectInput{Name: "pilot", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	cleanupProject(t, db, project.ID)
	scenario := approvedScenario(t, store, project.ID)
	makeRun := func() qa.Run {
		run, err := store.CreateRun(ctx, project.ID, qa.RunInput{ScenarioIDs: []string{scenario.ID}, Mode: "advisory"})
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	first := makeRun()
	_, firstToken, _, found, err := store.ClaimRun(ctx, "worker")
	if err != nil || !found {
		t.Fatalf("claim: %v", err)
	}
	cancelled, err := store.CancelRun(ctx, first.ID)
	if err != nil || cancelled.Status != "cancelled" || cancelled.Gate != "warn" {
		t.Fatalf("cancel: %+v %v", cancelled, err)
	}
	if _, err := store.CompleteRun(ctx, first.ID, qa.CompleteInput{LeaseToken: firstToken, Results: []qa.ScenarioResult{{ScenarioID: scenario.ID, Status: "passed"}}}); !errors.Is(err, qa.ErrConflict) {
		t.Fatalf("completion after cancel: %v", err)
	}
	second := makeRun()
	_, secondToken, _, found, err := store.ClaimRun(ctx, "worker")
	if err != nil || !found {
		t.Fatalf("claim: %v", err)
	}
	if _, err := db.Exec(ctx, `UPDATE runs SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Heartbeat(ctx, second.ID, secondToken); !errors.Is(err, qa.ErrConflict) {
		t.Fatalf("expired heartbeat: %v", err)
	}
	expired, err := store.GetRun(ctx, second.ID)
	if err != nil || expired.Status != "error" || expired.Gate != "warn" {
		t.Fatalf("expired run: %+v %v", expired, err)
	}
	if _, err := store.CompleteRun(ctx, second.ID, qa.CompleteInput{LeaseToken: secondToken, Results: []qa.ScenarioResult{{ScenarioID: scenario.ID, Status: "passed"}}}); !errors.Is(err, qa.ErrConflict) {
		t.Fatalf("completion after expiry: %v", err)
	}
}

func TestConcurrentClaimsAreUnique(t *testing.T) {
	store, db := testStore(t)
	ctx := context.Background()
	project, err := store.CreateProject(ctx, qa.ProjectInput{Name: "pilot", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	cleanupProject(t, db, project.ID)
	scenario := approvedScenario(t, store, project.ID)
	run, err := store.CreateRun(ctx, project.ID, qa.RunInput{ScenarioIDs: []string{scenario.ID}, Mode: "blocking"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, _, _, found, err := store.ClaimRun(ctx, "worker")
			results <- err == nil && found && claimed.ID == run.ID
		}()
	}
	wg.Wait()
	close(results)
	count := 0
	for found := range results {
		if found {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected one claim, got %d", count)
	}
}
