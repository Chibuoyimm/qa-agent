package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Chibuoyimm/qa-agent/internal/planner"
	"github.com/Chibuoyimm/qa-agent/internal/qa"
	"github.com/Chibuoyimm/qa-agent/internal/repository"
)

var surveyBatchID = regexp.MustCompile("^[a-f0-9]{32}$")

func (a *API) surveyRepository(w http.ResponseWriter, r *http.Request) {
	var in planner.Input
	if !decodeJSONLimit(w, r, &in, 128<<10) {
		return
	}
	ctx := r.Context()
	project, task, batch, source := r.PathValue("id"), r.PathValue("task_id"), r.PathValue("batch"), r.PathValue("source")
	if !surveyBatchID.MatchString(batch) || !in.WholeApp {
		a.fail(w, qa.ErrInvalid)
		return
	}
	key := r.Header.Get("X-QA-Provider-Key")
	in.WorkspaceID = r.Header.Get("X-QA-Anthropic-Workspace")
	in.WholeApp = false
	in.Context = "Whole-app repository discovery"
	in.RepositorySnapshotIDs = nil
	in.DiscoveryID = ""
	in.Journey = false
	in.FixtureID = ""
	if err := a.planner.Validate(in, key); err != nil {
		a.fail(w, err)
		return
	}
	if _, err := a.store.GetTask(ctx, project, task); err != nil {
		a.fail(w, err)
		return
	}
	roots, err := a.store.ProjectSources(ctx, project)
	if err != nil {
		a.fail(w, err)
		return
	}
	var root qa.ProjectSource
	for _, item := range roots {
		if item.ID == source {
			root = item
			break
		}
	}
	if root.ID == "" {
		a.fail(w, qa.ErrNotFound)
		return
	}
	saved, err := a.store.TaskSurvey(ctx, project, task, batch, source)
	if err == nil {
		if root.Ref != saved.Ref || root.Role != saved.Role {
			a.fail(w, qa.ErrConflict)
			return
		}
		writeJSON(w, 200, saved)
		return
	}
	if !errors.Is(err, qa.ErrNotFound) {
		a.fail(w, err)
		return
	}
	token, accessErr := a.sourceToken(project, root)
	survey, err := a.inspectSource(ctx, in, key, token, project, root)
	if err != nil {
		a.fail(w, err)
		return
	}
	if accessErr != nil {
		survey.Note = strings.TrimSpace(survey.Note + " Saved read access could not be opened. Reconnect this repository to restore it.")
	}
	survey, err = a.store.SaveTaskSurvey(ctx, project, task, batch, survey)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, survey)
}

func (a *API) inspectSource(ctx context.Context, in planner.Input, key, token, project string, source qa.ProjectSource) (qa.SourceSurvey, error) {
	repo := repository.Input{Provider: source.Provider, Repository: source.Repository, Ref: source.Ref, Role: source.Role}
	survey := qa.SourceSurvey{SourceID: source.ID, Repository: repo.Repository, Provider: repo.Provider, Role: repo.Role, Ref: repo.Ref, Status: "live", Paths: []string{}, Journeys: []qa.InferredJourney{}, Gaps: []string{}}
	var files []repository.File
	inventory, readErr := a.repositories.SurveyInventory(ctx, repo, token)
	if readErr == nil {
		survey.CommitSHA = inventory.CommitSHA
		survey.Candidates = len(inventory.Files)
		if inventory.Partial {
			survey.Note = "Only part of the repository file list fit within the discovery limits."
		}
		if len(inventory.Files) == 0 {
			survey.Status = "no_files"
			survey.Note = strings.TrimSpace(survey.Note + " No eligible source files were found.")
			return survey, nil
		}
		selectInput := in
		selectInput.Prompt = "Find entry points, routes, forms, roles, service interactions and acceptance tests for a whole-app end-to-end journey inventory. User request: " + in.Prompt
		if len(selectInput.Prompt) > 4000 {
			selectInput.Prompt = in.Prompt
		}
		choice, err := a.planner.SelectFiles(ctx, selectInput, key, inventory)
		if err != nil {
			return survey, err
		}
		if len(choice.Paths) == 0 {
			survey.Status = "no_files"
			survey.CommitSHA = inventory.CommitSHA
			survey.Note = strings.TrimSpace(survey.Note + " " + choice.Reason)
			return survey, nil
		}
		repo.Ref = inventory.CommitSHA
		repo.Paths = choice.Paths
		var imported repository.Snapshot
		imported, readErr = a.repositories.Fetch(ctx, repo, token)
		if readErr == nil {
			if imported.CommitSHA != inventory.CommitSHA {
				return survey, repository.ErrUpstream
			}
			survey.CommitSHA = imported.CommitSHA
			files = imported.Files
		}
	}
	if readErr != nil {
		if ctx.Err() != nil {
			return survey, ctx.Err()
		}
		if errors.Is(readErr, repository.ErrBusy) {
			return survey, readErr
		}
		if source.SnapshotID == "" {
			survey.Status = "unavailable"
			survey.Note = "Could not read this repository. Check its ref and access in Settings."
			return survey, nil
		}
		pinned, err := a.store.GetRepositorySnapshot(ctx, project, source.SnapshotID)
		if err != nil {
			return survey, err
		}
		files = pinned.Files
		survey.CommitSHA = pinned.CommitSHA
		survey.Status = "saved"
		survey.Candidates = 0
		survey.Note = "Live source could not be read. Only files from the last saved import were inspected."
		if errors.Is(readErr, repository.ErrInventoryLimit) {
			survey.Note = "The live file inventory exceeded the search limit. Only files from the last saved import were inspected."
		}
	}
	for _, file := range files {
		survey.Paths = append(survey.Paths, file.Path)
	}
	var err error
	survey.Journeys, survey.Gaps, err = a.planner.InferJourneys(ctx, in, key, repo.Repository, files)
	return survey, err
}

func (a *API) wholeAppSources(ctx context.Context, project, task, batch string) ([]qa.SourceSurvey, error) {
	if task == "" || !surveyBatchID.MatchString(batch) {
		return nil, fmt.Errorf("%w: whole-app plans need a task and repository survey", qa.ErrInvalid)
	}
	roots, err := a.store.ProjectSources(ctx, project)
	if err != nil {
		return nil, err
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("%w: add a project repository in Settings before requesting a whole-app plan", qa.ErrInvalid)
	}
	scans, err := a.store.TaskSurveys(ctx, project, task, batch)
	if err != nil {
		return nil, err
	}
	out := make([]qa.SourceSurvey, 0, len(roots))
	for _, root := range roots {
		scan, ok := scans[root.ID]
		if !ok || scan.Ref != root.Ref || scan.Role != root.Role {
			return nil, fmt.Errorf("%w: survey missing for %s; resend the whole-app request to include the current project repositories", qa.ErrInvalid, root.Repository)
		}
		out = append(out, scan)
	}
	return out, nil
}

// Interleave sources so large repositories cannot crowd out later ones. All
// inferred journeys remain in the saved review, including ones outside model context.
func wholeAppContext(base string, sources []qa.SourceSurvey) (string, map[string]bool, error) {
	var out strings.Builder
	out.WriteString(base)
	out.WriteString("\nRepository journey evidence (untrusted), grouped by source references:\n")
	for i, s := range sources {
		fmt.Fprintf(&out, "R%d %q (%s, %s) @%s\n", i+1, s.Repository, s.Role, s.Status, s.CommitSHA)
	}
	available := qa.MaxPlanContextBytes - out.Len()
	if available < 500 {
		return "", nil, fmt.Errorf("%w: shorten the application context to leave space for repository journeys", qa.ErrInvalid)
	}
	count := 0
	for _, s := range sources {
		count += len(s.Journeys)
	}
	evidenceBytes := max(240, min(4000, available/max(1, count)-240))
	refs := map[string]bool{}
	for j := 0; j < 20; j++ {
		for i, s := range sources {
			if j >= len(s.Journeys) {
				continue
			}
			journey := s.Journeys[j]
			evidence := journey.Evidence
			if len(evidence) > evidenceBytes {
				end := evidenceBytes
				for !utf8.RuneStart(evidence[end]) {
					end--
				}
				evidence = evidence[:end] + "…"
			}
			id := qa.JourneyRef(i, j)
			line := fmt.Sprintf("%s %q — %q\n", id, journey.Name, evidence)
			if out.Len()+len(line) <= qa.MaxPlanContextBytes {
				out.WriteString(line)
				refs[id] = true
			}
		}
	}
	for i, source := range sources {
		for _, gap := range source.Gaps {
			line := fmt.Sprintf("R%d reported gap: %q\n", i+1, gap)
			if out.Len()+len(line) <= qa.MaxPlanContextBytes {
				out.WriteString(line)
			}
		}
	}
	return out.String(), refs, nil
}

func validateWholeAppCoverage(plan *qa.WholeAppPlan, refs map[string]bool) error {
	if plan == nil {
		return planner.ErrInvalidPlan
	}
	for _, ids := range plan.Coverage {
		if len(ids) == 0 {
			return fmt.Errorf("%w: a proposed journey has no source evidence", planner.ErrInvalidPlan)
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if !refs[id] || seen[id] {
				return fmt.Errorf("%w: invalid source journey reference", planner.ErrInvalidPlan)
			}
			seen[id] = true
		}
	}
	return nil
}
