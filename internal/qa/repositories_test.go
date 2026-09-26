package qa_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Chibuoyimm/qa-agent/internal/qa"
	"github.com/Chibuoyimm/qa-agent/internal/repository"
	"github.com/Chibuoyimm/qa-agent/migrations"
)

func TestRepositorySnapshotsAreScopedAndImmutable(t *testing.T) {
	store, db := testStore(t)
	ctx := context.Background()
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatalf("reapply migrations: %v", err)
	}
	first, err := store.CreateProject(ctx, qa.ProjectInput{Name: "first", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateProject(ctx, qa.ProjectInput{Name: "second", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	imported := repository.Snapshot{
		Repository: "owner/name", Ref: "main", Role: "frontend", CommitSHA: strings.Repeat("a", 40),
		ContentSHA256: strings.Repeat("b", 64), Files: []repository.File{{Path: "src/app.ts", Content: "old source"}}, TotalBytes: len("old source"),
	}
	old, err := store.CreateRepositorySnapshot(ctx, first.ID, imported)
	if err != nil {
		t.Fatal(err)
	}
	imported.CommitSHA = strings.Repeat("c", 40)
	imported.Files = []repository.File{{Path: "src/app.ts", Content: "new source"}}
	newer, err := store.CreateRepositorySnapshot(ctx, first.ID, imported)
	if err != nil {
		t.Fatal(err)
	}
	if old.ID == newer.ID || old.CreatedAt.IsZero() || newer.ProjectID != first.ID {
		t.Fatalf("snapshot metadata: old=%+v new=%+v", old, newer)
	}
	loaded, err := store.GetRepositorySnapshot(ctx, first.ID, old.ID)
	if err != nil || loaded.CommitSHA != strings.Repeat("a", 40) || loaded.Files[0].Content != "old source" {
		t.Fatalf("old snapshot changed: %+v %v", loaded, err)
	}
	listed, err := store.ListRepositorySnapshots(ctx, first.ID)
	if err != nil || len(listed) != 2 {
		t.Fatalf("list first: %+v %v", listed, err)
	}
	otherList, err := store.ListRepositorySnapshots(ctx, second.ID)
	if err != nil || len(otherList) != 0 {
		t.Fatalf("list second: %+v %v", otherList, err)
	}
	if _, err := store.GetRepositorySnapshot(ctx, second.ID, old.ID); !errors.Is(err, qa.ErrNotFound) {
		t.Fatalf("cross-project read: %v", err)
	}
	if _, err := store.SelectRepositorySnapshots(ctx, second.ID, []string{old.ID}); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("cross-project proposal: %v", err)
	}
	if _, err := store.SelectRepositorySnapshots(ctx, first.ID, []string{old.ID, old.ID}); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("duplicate ID: %v", err)
	}
	if _, err := store.SelectRepositorySnapshots(ctx, first.ID, []string{old.ID, newer.ID}); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("duplicate role: %v", err)
	}
	if _, err := store.SelectRepositorySnapshots(ctx, first.ID, []string{old.ID, newer.ID, "third"}); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("too many IDs: %v", err)
	}
	if _, err := store.SelectRepositorySnapshots(ctx, first.ID, []string{"missing"}); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("missing ID: %v", err)
	}
}
