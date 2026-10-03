package qa_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Chibuoyimm/qa-agent/internal/qa"
	"github.com/Chibuoyimm/qa-agent/internal/repository"
)

func TestRepositoryProvidersAndReleaseCompatibility(t *testing.T) {
	store, db := testStore(t)
	ctx := context.Background()
	project, err := store.CreateProject(ctx, qa.ProjectInput{Name: "providers", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	scenario := approvedScenario(t, store, project.ID)
	for _, provider := range []string{"github", "azure"} {
		t.Run(provider, func(t *testing.T) {
			name := "owner/name"
			if provider == "azure" {
				name = "https://dev.azure.com/org/project/_git/repo"
			}
			imported := repository.Snapshot{Provider: provider, Repository: name, Ref: "main", Role: "frontend", CommitSHA: strings.Repeat("a", 40), ContentSHA256: strings.Repeat("b", 64), Files: []repository.File{{Path: "README.md", Content: "hello"}}, TotalBytes: 5}
			snapshot, err := store.CreateRepositorySnapshot(ctx, project.ID, imported)
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := store.GetRepositorySnapshot(ctx, project.ID, snapshot.ID)
			if err != nil || loaded.Provider != provider || loaded.Files[0].Content != "hello" {
				t.Fatalf("provider persistence: %+v %v", loaded, err)
			}
			input := qa.ReleaseInput{DeploymentKey: provider + "-release", BaseURL: "http://localhost:4174", Mode: "advisory", ScenarioIDs: []string{scenario.ID}, Repositories: []qa.ReleaseRepositoryInput{{Provider: provider, SnapshotID: snapshot.ID, Repository: name, Role: "frontend", CommitSHA: imported.CommitSHA}}}
			release, err := store.CreateRelease(ctx, project.ID, input)
			if err != nil || len(release.Repositories) != 1 || repository.EffectiveProvider(release.Repositories[0].Provider) != provider {
				t.Fatalf("release provider: %+v %v", release, err)
			}
			if provider == "github" {
				// Hash exactly the pre-provider release identity to verify retries after an upgrade.
				oldIdentity := `{"base_url":"http://localhost:4174","mode":"advisory","scenario_ids":["` + scenario.ID + `"],"repositories":[{"repository":"owner/name","role":"frontend","commit_sha":"` + imported.CommitSHA + `","content_sha256":"` + imported.ContentSHA256 + `","paths":["README.md"]}]}`
				hash := sha256.Sum256([]byte(oldIdentity))
				var stored string
				if err := db.QueryRow(ctx, `SELECT request_sha256 FROM releases WHERE id=$1`, release.ID).Scan(&stored); err != nil || stored != hex.EncodeToString(hash[:]) {
					t.Fatalf("legacy hash changed: %s %v", stored, err)
				}
				input.Repositories[0].Provider = ""
			}
			retry, err := store.CreateRelease(ctx, project.ID, input)
			if err != nil || retry.ID != release.ID || retry.Run.ID != release.Run.ID {
				t.Fatalf("provider retry: %+v %v", retry, err)
			}
			input.Repositories[0].Provider = "other"
			if _, err := store.CreateRelease(ctx, project.ID, input); !errors.Is(err, qa.ErrInvalid) {
				t.Fatalf("unknown provider: %v", err)
			}
			input.Repositories[0].Provider = "github"
			if provider == "github" {
				input.Repositories[0].Provider = "azure"
			}
			if _, err := store.CreateRelease(ctx, project.ID, input); !errors.Is(err, qa.ErrInvalid) {
				t.Fatalf("changed provider accepted: %v", err)
			}
		})
	}
	listed, err := store.ListRepositorySnapshots(ctx, project.ID)
	if err != nil || len(listed) != 2 || listed[0].Provider != "azure" || listed[1].Provider != "github" {
		t.Fatalf("provider listing: %+v %v", listed, err)
	}
	// Existing writers without a provider keep creating GitHub snapshots.
	var defaultProvider string
	if err := db.QueryRow(ctx, `INSERT INTO repository_snapshots(id,project_id,repository,ref,role,commit_sha,content_sha256,files,file_count,total_bytes) VALUES('legacy',$1,'owner/repo','main','backend',$2,$3,$4,1,0) RETURNING provider`, project.ID, strings.Repeat("c", 40), strings.Repeat("d", 64), json.RawMessage(`[{"path":"empty.ts","content":""}]`)).Scan(&defaultProvider); err != nil || defaultProvider != "github" {
		t.Fatalf("legacy default: %s %v", defaultProvider, err)
	}
}
