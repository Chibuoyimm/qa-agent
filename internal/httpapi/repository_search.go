package httpapi

import (
	"fmt"
	"net/http"

	"github.com/Chibuoyimm/qa-agent/internal/planner"
	"github.com/Chibuoyimm/qa-agent/internal/qa"
	"github.com/Chibuoyimm/qa-agent/internal/repository"
)

func repositoryCredential(r *http.Request, provider string) (string, error) {
	github, azure := r.Header.Get("X-QA-GitHub-Token"), r.Header.Get("X-QA-Azure-PAT")
	if repository.EffectiveProvider(provider) == "azure" {
		if github != "" {
			return "", fmt.Errorf("%w: credential header does not match repository provider", repository.ErrInvalid)
		}
		return azure, nil
	}
	if azure != "" {
		return "", fmt.Errorf("%w: credential header does not match repository provider", repository.ErrInvalid)
	}
	return github, nil
}

func (a *API) searchRepository(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Repository repository.Input `json:"repository"`
		AI         planner.Input    `json:"ai"`
	}
	if !decodeJSONLimit(w, r, &in, 16<<10) {
		return
	}
	projectID := r.PathValue("id")
	if _, err := a.store.GetProject(r.Context(), projectID); err != nil {
		a.fail(w, err)
		return
	}
	token, err := repositoryCredential(r, in.Repository.Provider)
	if err != nil {
		a.fail(w, err)
		return
	}
	// Validate model access and consent before contacting the repository.
	in.AI.Context = "Repository file names and sizes"
	in.AI.RepositorySnapshotIDs = nil
	in.AI.DiscoveryID = ""
	in.AI.WorkspaceID = r.Header.Get("X-QA-Anthropic-Workspace")
	key := r.Header.Get("X-QA-Provider-Key")
	if err := a.planner.Validate(in.AI, key); err != nil {
		a.fail(w, err)
		return
	}
	inventory, err := a.repositories.Inventory(r.Context(), in.Repository, token)
	if err != nil {
		a.fail(w, err)
		return
	}
	selection, err := a.planner.SelectFiles(r.Context(), in.AI, key, inventory)
	if err != nil {
		a.fail(w, err)
		return
	}
	var snapshot *qa.RepositorySnapshot
	if len(selection.Paths) > 0 {
		// Pin retrieval to the same commit whose inventory the model inspected.
		originalRef := in.Repository.Ref
		in.Repository.Ref = inventory.CommitSHA
		in.Repository.Paths = selection.Paths
		imported, err := a.repositories.Fetch(r.Context(), in.Repository, token)
		if err != nil {
			a.fail(w, err)
			return
		}
		if imported.CommitSHA != inventory.CommitSHA {
			a.fail(w, repository.ErrUpstream)
			return
		}
		imported.Ref = originalRef
		saved, err := a.store.CreateRepositorySnapshot(r.Context(), projectID, imported)
		if err != nil {
			a.fail(w, err)
			return
		}
		snapshot = &saved
	}
	writeJSON(w, http.StatusOK, struct {
		Snapshot   *qa.RepositorySnapshot `json:"snapshot"`
		Reason     string                 `json:"reason"`
		Candidates int                    `json:"candidates"`
		Excluded   int                    `json:"excluded"`
	}{snapshot, selection.Reason, len(inventory.Files), inventory.Excluded})
}
