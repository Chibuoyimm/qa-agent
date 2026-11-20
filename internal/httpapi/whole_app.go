package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
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
	work, err := a.store.TaskSurvey(ctx, project, task, batch, source)
	previous := -1
	if err == nil {
		if root.Ref != work.Ref || root.Role != work.Role {
			a.fail(w, qa.ErrConflict)
			return
		}
		if work.Status != "scanning" {
			writeJSON(w, 200, work.SourceSurvey)
			return
		}
		previous = work.BatchesRead
	} else if !errors.Is(err, qa.ErrNotFound) {
		a.fail(w, err)
		return
	}
	token, accessErr := a.sourceToken(project, root)
	if previous < 0 {
		work, err = a.startSourceSurvey(ctx, in, key, token, project, root)
		if err == nil && accessErr != nil {
			work.Note = strings.TrimSpace(work.Note + " Saved access could not be opened. Reconnect this repository to restore it.")
		}
	}
	if err == nil && previous < 0 && len(work.Pending) > 1 {
		work.Status = "scanning"
	} else if err == nil && len(work.Pending) > 0 {
		err = a.readSurveyBatch(ctx, in, key, token, &work)
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	result, err := a.store.SaveTaskSurvey(ctx, project, task, batch, work, previous)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func (a *API) startSourceSurvey(ctx context.Context, in planner.Input, key, token, project string, source qa.ProjectSource) (qa.SurveyWork, error) {
	repo := repository.Input{Provider: source.Provider, Repository: source.Repository, Ref: source.Ref, Role: source.Role}
	work := qa.SurveyWork{SourceSurvey: qa.SourceSurvey{SourceID: source.ID, Repository: repo.Repository, Provider: repo.Provider, Role: repo.Role, Ref: repo.Ref, Status: "live", Paths: []string{}, Journeys: []qa.InferredJourney{}, Gaps: []string{}}}
	inventory, err := a.repositories.SurveyInventory(ctx, repo, token)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, repository.ErrBusy) {
			return work, err
		}
		if source.SnapshotID == "" {
			work.Status = "unavailable"
			work.Note = "Could not read the repository inventory. Check provider availability, the ref and read access."
			return work, nil
		}
		pinned, err := a.store.GetRepositorySnapshot(ctx, project, source.SnapshotID)
		if err != nil {
			return work, err
		}
		work.Status = "saved"
		work.Note = "Live source could not be read. Only the last saved import was inspected."
		work.CommitSHA = pinned.CommitSHA
		err = a.inferSurveyFiles(ctx, in, key, &work, pinned.Files)
		return work, err
	}
	work.CommitSHA = inventory.CommitSHA
	work.Candidates = len(inventory.Files)
	work.InventoryExcluded = inventory.Excluded
	if inventory.Partial {
		work.Note = "Only part of the file list fit within the inventory limits."
	}
	if len(inventory.Files) == 0 {
		work.Status = "no_files"
		work.Note = strings.TrimSpace(work.Note + " No eligible files were found.")
		return work, nil
	}
	selectionInput := in
	selectionInput.WholeApp = true
	choices, err := a.planner.SelectFiles(ctx, selectionInput, key, inventory)
	if err != nil {
		return work, err
	}
	if len(choices.Paths) == 0 {
		work.Status = "no_files"
		work.Note = strings.TrimSpace(work.Note + " " + choices.Reason)
		return work, nil
	}
	if choices.Unmatched > 0 {
		work.Note = strings.TrimSpace(work.Note + " Only distinct paths found in the repository were included.")
	}
	work.Pending, err = inventory.SurveyBatches(choices.Paths)
	work.Batches = len(work.Pending)
	return work, err
}

func (a *API) readSurveyBatch(ctx context.Context, in planner.Input, key, token string, work *qa.SurveyWork) error {
	repo := repository.Input{Provider: work.Provider, Repository: work.Repository, Ref: work.CommitSHA, Role: work.Role}
	files, excluded, err := a.repositories.ReadSurveyBatch(ctx, repo, token, work.Pending[0])
	if err != nil {
		return err
	}
	work.ExcludedContent += excluded
	if len(files) > 0 {
		if err := a.inferSurveyFiles(ctx, in, key, work, files); err != nil {
			return err
		}
	}
	work.BatchesRead++
	work.Pending = work.Pending[1:]
	work.Status = "scanning"
	if len(work.Pending) == 0 {
		work.Status = "live"
		if len(work.Paths) == 0 {
			work.Status = "no_files"
		}
		if work.ExcludedContent > 0 {
			work.Note = strings.TrimSpace(work.Note + fmt.Sprintf(" %d selected files were excluded by source-content checks.", work.ExcludedContent))
		}
	}
	return nil
}

func (a *API) inferSurveyFiles(ctx context.Context, in planner.Input, key string, work *qa.SurveyWork, files []repository.File) error {
	journeys, gaps, err := a.planner.InferJourneys(ctx, in, key, work.Repository, files)
	if err != nil {
		return err
	}
	for _, f := range files {
		work.Paths = append(work.Paths, f.Path)
	}
	work.Journeys = append(work.Journeys, journeys...)
	for _, gap := range gaps {
		if !slices.Contains(work.Gaps, gap) {
			work.Gaps = append(work.Gaps, gap)
		}
	}
	return nil
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
		if !ok || scan.Ref != root.Ref || scan.Role != root.Role || scan.Status == "scanning" {
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
		fmt.Fprintf(&out, "R%d %q (%s, %s) @%s. Read %d files; eligible inventory: %d. %s\n", i+1, s.Repository, s.Role, s.Status, s.CommitSHA, len(s.Paths), s.Candidates, s.Note)
	}
	remaining := 0
	longest := 0
	gaps := false
	for _, source := range sources {
		remaining += len(source.Journeys)
		longest = max(longest, len(source.Journeys))
		gaps = gaps || len(source.Gaps) > 0
	}
	limit := qa.MaxSurveyContextBytes
	if gaps {
		limit -= min(8000, (limit-out.Len())/10)
	}
	if limit-out.Len() < 500 {
		return "", nil, fmt.Errorf("%w: insufficient space for the repository survey", qa.ErrInvalid)
	}
	refs := map[string]bool{}
	for j := 0; j < longest; j++ {
		for i, source := range sources {
			if j >= len(source.Journeys) {
				continue
			}
			id := qa.JourneyRef(i, j)
			record := source.Journeys[j]
			share := (limit - out.Len()) / remaining
			remaining--
			if share < len(id)+16 {
				continue
			}
			name := surveyExcerpt(record.Name, min(305, share/2))
			prefix := id + " " + name + " — "
			line := prefix + surveyExcerpt(record.Evidence, share-len(prefix)-1) + "\n"
			out.WriteString(line)
			refs[id] = true
		}
	}
	for i, source := range sources {
		for _, gap := range source.Gaps {
			line := fmt.Sprintf("R%d reported gap: %q\n", i+1, gap)
			if out.Len()+len(line) <= qa.MaxSurveyContextBytes {
				out.WriteString(line)
			}
		}
	}
	return out.String(), refs, nil
}

// Preserve the entry point and the outcome when a large survey needs shorter evidence.
func surveyExcerpt(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	if limit < 4 {
		return ""
	}
	head := (limit - 3) / 2
	tail := len(value) - (limit - 3 - head)
	for head > 0 && !utf8.RuneStart(value[head]) {
		head--
	}
	for tail < len(value) && !utf8.RuneStart(value[tail]) {
		tail++
	}
	return value[:head] + "…" + value[tail:]
}

func validateWholeAppCoverage(plan *qa.WholeAppPlan, refs map[string]bool) error {
	if plan == nil {
		return planner.ErrInvalidPlan
	}
	groups := slices.Clone(plan.Coverage)
	for _, journey := range plan.NeedsSetup {
		groups = append(groups, journey.SourceRefs)
	}
	for _, ids := range groups {
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
