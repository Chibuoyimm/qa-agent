package httpapi

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/Chibuoyimm/qa-agent/internal/planner"
	"github.com/Chibuoyimm/qa-agent/internal/qa"
	"github.com/Chibuoyimm/qa-agent/internal/repository"
	"golang.org/x/sync/errgroup"
)

func (a *API) resolveJourneyChat(w http.ResponseWriter, r *http.Request, req chatProposalInput, fingerprint string) {
	// A scoped resolution can involve multiple repository reads and model calls.
	ctx, cancel := context.WithTimeout(r.Context(), qa.SetupResolutionTimeout)
	defer cancel()
	if deadline, ok := ctx.Deadline(); ok {
		_ = http.NewResponseController(w).SetWriteDeadline(deadline.Add(5 * time.Second))
	}
	project, taskID := r.PathValue("id"), r.PathValue("task_id")
	in := req.Input
	if taskID == "" || !in.Consent || in.WholeApp || in.FixtureID != "" || len(req.Resolve.Reference) > 400 || len(in.Context) > 4000 {
		a.fail(w, qa.ErrInvalid)
		return
	}
	task, err := a.store.GetTask(ctx, project, taskID)
	if err != nil {
		a.fail(w, err)
		return
	}
	target, err := a.store.GetProject(ctx, project)
	if err != nil {
		a.fail(w, err)
		return
	}
	if req.Resolve.Target != target.BaseURL {
		a.fail(w, qa.ErrConflict)
		return
	}
	key := r.Header.Get("X-QA-Provider-Key")
	in.WorkspaceID = r.Header.Get("X-QA-Anthropic-Workspace")
	in.ResolveSetup = true
	in.Journey = true
	in.RepositorySnapshotIDs = nil
	in.DiscoveryID = ""
	in.Context = "Workspace target: " + target.BaseURL + "\nAdditional details: " + in.Context
	if err = a.planner.Validate(in, key); err != nil {
		a.fail(w, err)
		return
	}
	turn, fresh, err := a.store.BeginTaskChat(ctx, project, taskID, req.RequestID, req.Prompt, "resolve:"+fingerprint)
	if err != nil {
		a.fail(w, err)
		return
	}
	if !fresh {
		writeJSON(w, 200, turn)
		return
	}
	fail := func(e error) {
		failure, reply := planningFailure(e)
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		a.logger.Warn("setup resolution failed", "request_id", req.RequestID, "failure", failure.Code, "detail", failure.Detail)
		if _, err := a.store.FailChat(cleanup, project, req.RequestID, reply, failure); err != nil {
			a.logger.Error("close setup resolution", "error", err)
		}
		a.fail(w, e)
	}
	id, question, err := a.store.SetupJourney(ctx, project, task, req.SavedFlowID, req.Resolve.Reference)
	if err != nil {
		fail(err)
		return
	}
	receipt := &qa.SetupResolution{Target: target.BaseURL, Sources: []qa.SetupSource{}}
	result := qa.Proposal{Provider: in.Provider, Model: in.Model, CredentialMode: in.CredentialMode, Scenarios: []qa.ScenarioInput{}, Questions: []string{}, Assumptions: []string{}}
	if question != "" {
		result.Questions = []string{question}
	} else {
		in, goal, err := a.journeySetupContext(ctx, project, task, target.BaseURL, id, in)
		if err != nil {
			fail(err)
			return
		}
		result, err = a.resolveFromSources(ctx, project, turn.ID, in, key, goal, receipt)
		if err != nil {
			fail(err)
			return
		}
	}
	result.Resolution = receipt
	result.SavedFlowID = id
	if len(result.Questions) > 0 {
		result.Scenarios = []qa.ScenarioInput{}
		turn, err = a.store.FinishChat(ctx, project, turn.ID, "I need the following before I can run this journey.", &result)
	} else {
		result.FlowVersion = turn.ID
		turn, err = a.store.FinishResolvedRun(ctx, project, taskID, turn.ID, target.BaseURL, result)
	}
	if err != nil {
		fail(err)
		return
	}
	writeJSON(w, 200, turn)
}

func (a *API) journeySetupContext(ctx context.Context, project string, task qa.TestingTask, target, id string, in planner.Input) (planner.Input, string, error) {
	saved, err := a.store.SavedJourneyPlan(ctx, project, id)
	if err != nil {
		return in, "", err
	}
	goal := task.Request
	if saved != nil {
		goal = saved.ExpectedOutcome
		in.Context += "\nPreviously missing setup (re-evaluate using current evidence): " + strings.Join(saved.MissingSetup, "; ")
	}
	in.Context += "\nRequested business outcome: " + goal
	page, err := a.store.ListTaskChat(ctx, project, task.ID, 0)
	if err != nil {
		return in, "", err
	}
	historyBytes := 0
	for i := len(page.Turns) - 1; i >= 0; i-- {
		t := page.Turns[i]
		if t.Proposal != nil && t.Proposal.SavedFlowID == id && t.Proposal.Resolution != nil && len(t.Proposal.Questions) > 0 {
			entry := "\nPrior setup exchange: " + t.Prompt + "\nQuestions at that point: " + strings.Join(t.Proposal.Questions, "; ")
			if historyBytes+len(entry) > 12000 {
				break
			}
			in.Context += entry
			historyBytes += len(entry)
		}
	}
	for _, p := range a.FixtureProfiles {
		if slices.Contains(p.Origins, target) {
			in.SetupProfiles = append(in.SetupProfiles, p)
		}
	}
	meta, _ := json.Marshal(in.SetupProfiles)
	in.Context += "\nAvailable installed setup profiles (public metadata): " + string(meta)
	access, err := a.store.GetVerifiedAccess(ctx, project)
	if err != nil {
		return in, "", err
	}
	if access.DiscoveryID != "" {
		discovery, err := a.store.SelectDiscovery(ctx, project, access.DiscoveryID)
		if err != nil {
			return in, "", err
		}
		if discovery.BaseURL == target {
			in.Context, err = assembleProposalContext(in.Context, nil, &discovery)
			if err != nil {
				return in, "", err
			}
			if discovery.SetupScenario != nil {
				for _, step := range discovery.SetupScenario.Steps {
					if step.SecretEnv != nil {
						in.AccessRefs = append(in.AccessRefs, *step.SecretEnv)
					}
				}
			}
		}
	}
	return in, goal, nil
}

func (a *API) resolveFromSources(ctx context.Context, project, turnID string, in planner.Input, key, goal string, receipt *qa.SetupResolution) (qa.Proposal, error) {
	result := qa.Proposal{Provider: in.Provider, Model: in.Model, CredentialMode: in.CredentialMode, Scenarios: []qa.ScenarioInput{}, Assumptions: []string{}, Questions: []string{}}
	roots, err := a.store.ProjectSources(ctx, project)
	if err != nil {
		return result, err
	}
	if len(roots) == 0 {
		result.Questions = []string{"Which repository implements this journey? Connect it under Settings, then ask me to resolve setup again."}
		return result, nil
	}
	budget := (265000 - len(in.Context)) / len(roots)
	if err = a.store.UpdateSetupProgress(ctx, project, turnID, fmt.Sprintf("Reading %d connected repositories for this journey…", len(roots))); err != nil {
		return result, err
	}
	sources, evidence, err := a.readSetupSources(ctx, project, roots, in, key, goal, budget, nil)
	if err != nil {
		return result, err
	}
	receipt.Sources = sources
	filesRead := 0
	for i, source := range receipt.Sources {
		filesRead += len(source.Paths)
		in.Context += "\nRepository: " + source.Repository + " (" + roots[i].Role + ") " + source.Note + "\n" + evidence[i]
	}
	if filesRead == 0 {
		result.Questions = []string{"I could not read relevant source from the connected repositories. Can you confirm the repository, revision and read access for this journey?"}
		return result, nil
	}
	msg := fmt.Sprintf("Read %d files across %d repositories. Checking the journey against available setup…", filesRead, len(roots))
	if err = a.store.UpdateSetupProgress(ctx, project, turnID, msg); err != nil {
		return result, err
	}
	result, err = a.proposeResolved(ctx, in, key)
	if err != nil || len(result.Questions) == 0 {
		return result, err
	}
	if err = a.store.UpdateSetupProgress(ctx, project, turnID, "Checking additional source files before asking you: "+strings.Join(result.Questions, " ")); err != nil {
		return result, err
	}
	focus := strings.Join(result.Questions, "; ") + " " + goal
	budget = min(72000, (340000-len(in.Context))/len(roots))
	more, details, err := a.readSetupSources(ctx, project, roots, in, key, focus, budget, receipt.Sources)
	if err != nil {
		return result, err
	}
	inspected := 0
	for i, part := range more {
		first := &receipt.Sources[i]
		if part.Commit != first.Commit || len(part.Paths) == 0 {
			continue
		}
		for _, path := range part.Paths {
			if !slices.Contains(first.Paths, path) {
				first.Paths = append(first.Paths, path)
			}
		}
		inspected += len(part.Paths)
		in.Context += "\nFocused source evidence from " + roots[i].Repository + "\n" + details[i]
	}
	if inspected == 0 {
		return result, nil
	}
	if err = a.store.UpdateSetupProgress(ctx, project, turnID, fmt.Sprintf("Checked %d files for the remaining setup questions…", inspected)); err != nil {
		return result, err
	}
	in.Context += "\nRe-evaluate the outstanding questions against all source evidence: " + strings.Join(result.Questions, "; ")
	return a.proposeResolved(ctx, in, key)
}

// Revisit files at the same pinned revisions on a focused pass. An initial
// summary may have omitted the exact detail the follow-up is looking for.
func (a *API) readSetupSources(ctx context.Context, project string, roots []qa.ProjectSource, in planner.Input, key, focus string, budget int, prior []qa.SetupSource) ([]qa.SetupSource, []string, error) {
	sources := make([]qa.SetupSource, len(roots))
	evidence := make([]string, len(roots))
	group, readCtx := errgroup.WithContext(ctx)
	group.SetLimit(2)
	for i, root := range roots {
		if prior != nil {
			if prior[i].Commit == "" {
				continue
			}
			root.Ref = prior[i].Commit
		}
		group.Go(func() error {
			var err error
			sources[i], evidence[i], err = a.readSetupSource(readCtx, project, root, in, key, focus, budget, prior != nil)
			return err
		})
	}
	return sources, evidence, group.Wait()
}

func (a *API) readSetupSource(ctx context.Context, project string, root qa.ProjectSource, in planner.Input, key, goal string, budget int, focused bool) (qa.SetupSource, string, error) {
	source := qa.SetupSource{Repository: root.Repository, Paths: []string{}}
	repo := repository.Input{Repository: root.Repository, Provider: root.Provider, Role: root.Role, Ref: root.Ref}
	token, err := a.sourceToken(project, root)
	var inventory repository.Inventory
	if err == nil {
		inventory, err = a.repositories.SurveyInventory(ctx, repo, token)
	}
	var files []repository.File
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, repository.ErrBusy) {
			return source, "", err
		}
		if root.SnapshotID == "" {
			source.Note = "Live source unavailable. Check read access and revision."
			return source, "", nil
		}
		snapshot, err := a.store.GetRepositorySnapshot(ctx, project, root.SnapshotID)
		if err != nil {
			return source, "", err
		}
		source.Commit = snapshot.CommitSHA
		source.Note = "Used the saved source snapshot."
		files = snapshot.Files
	} else if len(inventory.Files) > 0 {
		source.Commit = inventory.CommitSHA
		repo.Ref = source.Commit

		if len(inventory.Files) == 0 {
			return source, "", nil
		}
		in.Prompt = goal
		if len(in.Prompt) > 4000 {
			in.Prompt = strings.ToValidUTF8(in.Prompt[:4000], "")
		}
		sizes := make(map[string]int, len(inventory.Files))
		ranked := make([]string, 0, len(inventory.Files))
		cost := 0
		for _, f := range inventory.Files {
			sizes[f.Path] = f.Size
			ranked = append(ranked, f.Path)
			cost += f.Size + len(f.Path) + 20
		}
		if cost > budget || focused {
			selected, err := a.planner.SelectFiles(ctx, in, key, inventory)
			if err != nil && !errors.Is(err, planner.ErrFileSelection) {
				return source, "", err
			}
			if len(selected.Paths) > 0 {
				ranked = selected.Paths
			} else {
				// Even an invalid AI selection can be recovered using actual inventory paths.
				ranks := map[string]int{}
				words := strings.FieldsFunc(strings.ToLower(goal), func(r rune) bool { return !unicode.IsLetter(r) })
				for _, path := range ranked {
					name := strings.ToLower(path)
					for _, word := range words {
						if len(word) > 3 && strings.Contains(name, strings.TrimSuffix(word, "s")) {
							ranks[path] += 3
						}
					}
					for _, term := range []string{"route", "page", "form", "schema", "service", "login", "auth", "proxy", "api", "test"} {
						if strings.Contains(name, term) {
							ranks[path]++
						}
					}
				}
				slices.SortStableFunc(ranked, func(x, y string) int { return cmp.Compare(ranks[y], ranks[x]) })
			}
		}
		used := 0
		paths := []string{}
		for _, p := range ranked {
			cost := sizes[p] + len(p) + 20
			if cost <= budget-used {
				used += cost
				paths = append(paths, p)
			}
		}
		batches, err := inventory.SurveyBatches(paths)
		if err != nil {
			return source, "", err
		}
		for _, b := range batches {
			part, _, err := a.repositories.ReadSurveyBatch(ctx, repo, token, b)
			if err != nil {
				return source, "", err
			}
			files = append(files, part...)
		}
	}
	var body strings.Builder
	for _, f := range files {
		if body.Len()+len(f.Content)+len(f.Path)+20 <= budget {
			fmt.Fprintf(&body, "\nFile %s\n%s\n", f.Path, f.Content)
			source.Paths = append(source.Paths, f.Path)
		}
	}
	text := body.String()
	if len(text) > 24_000 && !focused {
		evidence, err := a.planner.SetupEvidence(ctx, in, key, root.Repository, goal, text)
		if err != nil {
			return source, "", err
		}
		if evidence != "" {
			text = evidence
		}
	}
	return source, text, nil
}

func (a *API) proposeResolved(ctx context.Context, in planner.Input, key string) (qa.Proposal, error) {
	result, err := a.planner.Propose(ctx, in, key)
	if errors.Is(err, planner.ErrInvalidPlan) {
		in.Context += "\nCorrect the plan contract before responding: " + err.Error()
		return a.planner.Propose(ctx, in, key)
	}
	return result, err
}
