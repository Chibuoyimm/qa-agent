package qa_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Chibuoyimm/qa-agent/internal/qa"
	"github.com/Chibuoyimm/qa-agent/internal/repository"
)

func TestReleaseAtomicIdempotencyAndScope(t *testing.T) {
	store, db := testStore(t)
	ctx := context.Background()
	store = qa.NewStore(db, map[string]bool{"http://localhost:4174": true, "http://localhost:4175": true})
	project, err := store.CreateProject(ctx, qa.ProjectInput{Name: "pilot", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateProject(ctx, qa.ProjectInput{Name: "other", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	scenario := approvedScenario(t, store, project.ID)
	draft, err := store.CreateScenario(ctx, project.ID, qa.ScenarioInput{Name: "draft", ExpectedOutcome: "draft", Steps: []qa.Step{{Action: "assert_visible", TestID: "draft"}}})
	if err != nil {
		t.Fatal(err)
	}
	imported := repository.Snapshot{
		Repository: "owner/frontend", Ref: strings.Repeat("a", 40), Role: "frontend", CommitSHA: strings.Repeat("a", 40),
		ContentSHA256: strings.Repeat("b", 64), Files: []repository.File{{Path: "src/z.ts", Content: "z"}, {Path: "src/a.ts", Content: "a"}}, TotalBytes: 2,
	}
	first, err := store.CreateRepositorySnapshot(ctx, project.ID, imported)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateRepositorySnapshot(ctx, project.ID, imported)
	if err != nil {
		t.Fatal(err)
	}
	wrongProject, err := store.CreateRepositorySnapshot(ctx, other.ID, imported)
	if err != nil {
		t.Fatal(err)
	}
	input := qa.ReleaseInput{
		DeploymentKey: "production-12345", BaseURL: "http://localhost:4175", Mode: "blocking", ScenarioIDs: []string{scenario.ID},
		Repositories: []qa.ReleaseRepositoryInput{{SnapshotID: first.ID, Repository: imported.Repository, Role: imported.Role, CommitSHA: imported.CommitSHA}},
	}
	invalid := input
	invalid.DeploymentKey = "invalid-draft"
	invalid.ScenarioIDs = []string{draft.ID}
	if _, err := store.CreateRelease(ctx, project.ID, invalid); !errors.Is(err, qa.ErrConflict) {
		t.Fatalf("unapproved scenario: %v", err)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM releases WHERE project_id=$1`, project.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("release rollback: count=%d err=%v", count, err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM runs WHERE project_id=$1`, project.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("run rollback: count=%d err=%v", count, err)
	}
	invalid = input
	invalid.Repositories = []qa.ReleaseRepositoryInput{{SnapshotID: wrongProject.ID, Repository: imported.Repository, Role: imported.Role, CommitSHA: imported.CommitSHA}}
	if _, err := store.CreateRelease(ctx, project.ID, invalid); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("wrong-project source: %v", err)
	}
	invalid = input
	invalid.BaseURL = "http://localhost:4175/"
	if _, err := store.CreateRelease(ctx, project.ID, invalid); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("non-exact origin: %v", err)
	}
	invalid.BaseURL = "http://unapproved.example.test"
	if _, err := store.CreateRelease(ctx, project.ID, invalid); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("unapproved origin: %v", err)
	}
	var wg sync.WaitGroup
	type outcome struct {
		release qa.Release
		err     error
	}
	outcomes := make(chan outcome, 8)
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			attempt := input
			if i%2 == 1 {
				attempt.Repositories = []qa.ReleaseRepositoryInput{{SnapshotID: second.ID, Repository: imported.Repository, Role: imported.Role, CommitSHA: imported.CommitSHA}}
			}
			release, err := store.CreateRelease(ctx, project.ID, attempt)
			outcomes <- outcome{release, err}
		}()
	}
	wg.Wait()
	close(outcomes)
	var release qa.Release
	for result := range outcomes {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if release.ID == "" {
			release = result.release
		}
		if result.release.ID != release.ID || result.release.Run.ID != release.Run.ID {
			t.Fatalf("concurrent retry created another release/run: %+v", result.release)
		}
	}
	if release.BaseURL != "http://localhost:4175" || release.Run.BaseURL != release.BaseURL || len(release.Repositories) != 1 ||
		strings.Join(release.Repositories[0].Paths, ",") != "src/a.ts,src/z.ts" || len(release.Run.Scenarios) != 1 {
		t.Fatalf("release contents: %+v", release)
	}
	unchanged, err := store.GetProject(ctx, project.ID)
	if err != nil || unchanged.BaseURL != "http://localhost:4174" {
		t.Fatalf("project default changed: %+v %v", unchanged, err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM releases WHERE project_id=$1`, project.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("one release: count=%d err=%v", count, err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM runs WHERE project_id=$1`, project.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("one run: count=%d err=%v", count, err)
	}
	if _, err := db.Exec(ctx, `UPDATE scenarios SET expected_outcome='changed expectation',approved=false,
		steps='[{"action":"assert_visible","test_id":"different"}]'::jsonb WHERE id=$1`, scenario.ID); err != nil {
		t.Fatal(err)
	}
	frozen, err := store.GetRelease(ctx, project.ID, release.ID)
	if err != nil || frozen.Run.Scenarios[0].ExpectedOutcome != scenario.ExpectedOutcome || frozen.Run.Scenarios[0].Steps[1].TestID != "revenue" {
		t.Fatalf("frozen scenario after edit: %+v %v", frozen.Run.Scenarios, err)
	}
	newExecution := input
	newExecution.DeploymentKey = "new-execution"
	if _, err := store.CreateRelease(ctx, project.ID, newExecution); !errors.Is(err, qa.ErrConflict) {
		t.Fatalf("new execution must check current approval: %v", err)
	}
	uppercase := input
	uppercase.Repositories = []qa.ReleaseRepositoryInput{{SnapshotID: second.ID, Repository: imported.Repository, Role: imported.Role, CommitSHA: strings.ToUpper(imported.CommitSHA)}}
	upperRetry, err := store.CreateRelease(ctx, project.ID, uppercase)
	if err != nil || upperRetry.ID != release.ID || upperRetry.Run.Scenarios[0].ExpectedOutcome != scenario.ExpectedOutcome {
		t.Fatalf("uppercase commit retry: %+v %v", upperRetry, err)
	}
	changed := input
	changed.Mode = "advisory"
	if _, err := store.CreateRelease(ctx, project.ID, changed); !errors.Is(err, qa.ErrConflict) {
		t.Fatalf("changed mode under same key: %v", err)
	}
	changedContent := imported
	changedContent.ContentSHA256 = strings.Repeat("c", 64)
	changedContent.Files = []repository.File{{Path: "src/z.ts", Content: "different"}, {Path: "src/a.ts", Content: "a"}}
	changedContent.TotalBytes = len("different") + 1
	contentSnapshot, err := store.CreateRepositorySnapshot(ctx, project.ID, changedContent)
	if err != nil {
		t.Fatal(err)
	}
	changed = input
	changed.Repositories = []qa.ReleaseRepositoryInput{{SnapshotID: contentSnapshot.ID, Repository: imported.Repository, Role: imported.Role, CommitSHA: imported.CommitSHA}}
	if _, err := store.CreateRelease(ctx, project.ID, changed); !errors.Is(err, qa.ErrConflict) {
		t.Fatalf("changed source content: %v", err)
	}
	changedPaths := imported
	changedPaths.Files = []repository.File{{Path: "src/z.ts", Content: "z"}, {Path: "src/other.ts", Content: "a"}}
	pathSnapshot, err := store.CreateRepositorySnapshot(ctx, project.ID, changedPaths)
	if err != nil {
		t.Fatal(err)
	}
	changed.Repositories = []qa.ReleaseRepositoryInput{{SnapshotID: pathSnapshot.ID, Repository: imported.Repository, Role: imported.Role, CommitSHA: imported.CommitSHA}}
	if _, err := store.CreateRelease(ctx, project.ID, changed); !errors.Is(err, qa.ErrConflict) {
		t.Fatalf("changed selected paths: %v", err)
	}
	changed = input
	changed.Repositories = []qa.ReleaseRepositoryInput{{SnapshotID: second.ID, Repository: imported.Repository, Role: imported.Role, CommitSHA: strings.Repeat("c", 40)}}
	if _, err := store.CreateRelease(ctx, project.ID, changed); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("mismatched commit: %v", err)
	}
	if _, err := store.GetRelease(ctx, other.ID, release.ID); !errors.Is(err, qa.ErrNotFound) {
		t.Fatalf("cross-project release: %v", err)
	}
	if _, err := store.GetReleaseByKey(ctx, other.ID, input.DeploymentKey); !errors.Is(err, qa.ErrNotFound) {
		t.Fatalf("cross-project key: %v", err)
	}
	listed, err := store.ListReleases(ctx, project.ID)
	if err != nil || len(listed) != 1 || listed[0].Run.ID != release.Run.ID {
		t.Fatalf("release listing: %+v %v", listed, err)
	}
}
