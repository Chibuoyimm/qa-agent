package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Chibuoyimm/qa-agent/internal/planner"
	"github.com/Chibuoyimm/qa-agent/internal/qa"
)

func (a *API) listChat(w http.ResponseWriter, r *http.Request) {
	var before int64
	if raw := r.URL.Query().Get("before"); raw != "" {
		var err error
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before < 1 {
			writeError(w, 400, "before must be a positive message sequence")
			return
		}
	}
	taskID := r.PathValue("task_id")
	if taskID != "" {
		if _, err := a.store.GetTask(r.Context(), r.PathValue("id"), taskID); err != nil {
			a.fail(w, err)
			return
		}
	}
	page, err := a.store.ListTaskChat(r.Context(), r.PathValue("id"), taskID, before)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, page)
}

type chatProposalInput struct {
	SurveyBatch string `json:"survey_batch,omitempty"`
	planner.Input
	SavedFlowID  string `json:"saved_flow_id,omitempty"`
	RequestID    string `json:"request_id"`
	RunContextID string `json:"run_context_id,omitempty"`
}

func (a *API) chatProposal(w http.ResponseWriter, r *http.Request) {
	var request chatProposalInput
	if !decodeJSONLimit(w, r, &request, 128<<10) {
		return
	}
	if err := qa.ValidateChatRequest(request.RequestID, request.Prompt); err != nil {
		a.fail(w, err)
		return
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		a.fail(w, err)
		return
	}
	digest := sha256.Sum256(encoded)
	fingerprint := hex.EncodeToString(digest[:])
	taskID := r.PathValue("task_id")
	if request.SavedFlowID != "" && (taskID == "" || !request.Consent || request.WholeApp) {
		a.fail(w, qa.ErrInvalid)
		return
	}
	var preparedFlow string
	if taskID != "" {
		task, err := a.store.GetTask(r.Context(), r.PathValue("id"), taskID)
		if err != nil {
			a.fail(w, err)
			return
		}
		if request.Consent && !request.WholeApp {
			journey := task.JourneyID
			if request.SavedFlowID != "" {
				journey = request.SavedFlowID
			}
			saved, err := a.store.SavedJourneyPlan(r.Context(), r.PathValue("id"), journey)
			if err != nil {
				a.fail(w, err)
				return
			}
			if request.SavedFlowID != "" && saved == nil {
				a.fail(w, qa.ErrNotFound)
				return
			}
			if saved != nil {
				if request.SavedFlowID != "" || request.Prompt == task.Request {
					preparedFlow = journey
				}
				// Carry the saved requirements forward even when the new request has no chat history.
				request.Context += "\nSaved journey outcome (untrusted): " + saved.ExpectedOutcome
				for _, item := range saved.MissingSetup {
					request.Context += "\nSetup to verify: " + item
				}
			}
		}
		if request.DiscoveryID == "" && task.DiscoveryID != "" {
			request.DiscoveryID = task.DiscoveryID
		}
		if request.RunContextID == "" && preparedFlow == "" {
			page, err := a.store.ListTaskChat(r.Context(), r.PathValue("id"), taskID, 0)
			if err != nil {
				a.fail(w, err)
				return
			}
			for i := len(page.Turns) - 1; i >= 0; i-- {
				if page.Turns[i].RunID != "" {
					request.RunContextID = page.Turns[i].RunID
					break
				}
			}
		}
	}
	in := request.Input
	var surveys []qa.SourceSurvey
	if in.WholeApp {
		if !in.Consent || in.Journey {
			a.fail(w, qa.ErrInvalid)
			return
		}
		var err error
		surveys, err = a.wholeAppSources(r.Context(), r.PathValue("id"), taskID, request.SurveyBatch)
		if err != nil {
			a.fail(w, err)
			return
		}
		if strings.TrimSpace(in.Context) == "" {
			in.Context = "Plan the app's user-facing functional journeys."
		}
	}
	if request.RunContextID != "" {
		run, err := a.store.GetRun(r.Context(), request.RunContextID)
		if err != nil {
			a.fail(w, err)
			return
		}
		if run.ProjectID != r.PathValue("id") {
			a.fail(w, qa.ErrNotFound)
			return
		}
		in.Context, err = runContext(in.Context, run)
		if err != nil {
			a.fail(w, err)
			return
		}
	}
	// An empty first request is useful: the model must ask for requirements, not invent tests.
	if strings.TrimSpace(in.Context) == "" && len(in.RepositorySnapshotIDs) == 0 && in.DiscoveryID == "" {
		in.Context = "No application context supplied. Ask for missing business expectations, paths and accessible control names or test IDs before proposing executable checks."
	}
	key := r.Header.Get("X-QA-Provider-Key")
	in.WorkspaceID = r.Header.Get("X-QA-Anthropic-Workspace")
	projectID := r.PathValue("id")
	in, sources, err := a.prepareProposal(r.Context(), projectID, in, key)
	if err != nil {
		a.fail(w, err)
		return
	}
	turn, fresh, err := a.store.BeginTaskChat(r.Context(), projectID, taskID, request.RequestID, request.Prompt, fingerprint)
	if err != nil {
		a.fail(w, err)
		return
	}
	if !fresh {
		writeJSON(w, 200, turn)
		return
	}
	finished := false
	var planningErr error = context.Canceled
	defer func() {
		if finished {
			return
		}
		// Cancellation must still close the persisted pending turn; no inference is retried.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()
		failure, reply := planningFailure(planningErr)
		a.logger.Warn("planning failed", "project_id", projectID, "request_id", request.RequestID, "failure_code", failure.Code, "detail", failure.Detail)
		if _, err := a.store.FailChat(ctx, projectID, request.RequestID, reply, failure); err != nil {
			a.logger.Error("close interrupted chat request", "error", err)
		}
	}()
	page, err := a.store.ListTaskChat(r.Context(), projectID, taskID, 0)
	if err != nil {
		planningErr = err
		a.fail(w, err)
		return
	}
	if preparedFlow != "" {
		turns := page.Turns[:0]
		for _, t := range page.Turns {
			if t.Proposal != nil && t.Proposal.SavedFlowID == preparedFlow {
				turns = append(turns, t)
			}
		}
		page.Turns = turns
	}
	in.Context, err = chatContext(in.Context, page.Turns)
	if err != nil {
		planningErr = err
		a.fail(w, err)
		return
	}
	var journeyRefs map[string]bool
	if in.WholeApp {
		in.Context, journeyRefs, err = wholeAppContext(in.Context, surveys)
		in.SurveyContext = true
		if err != nil {
			planningErr = err
			a.fail(w, err)
			return
		}
	}
	if err := a.planner.Validate(in, key); err != nil {
		planningErr = err
		a.fail(w, err)
		return
	}
	var result qa.Proposal
	if in.WholeApp && len(journeyRefs) == 0 {
		hash := sha256.Sum256([]byte(in.Context))
		result = qa.Proposal{Provider: in.Provider, Model: in.Model, CredentialMode: in.CredentialMode, ChatGPTProfileID: in.ChatGPTProfileID, ContextSHA256: hex.EncodeToString(hash[:]), Scenarios: []qa.ScenarioInput{}, Questions: []string{"No runnable journey could be grounded in the source. Can you check the repository access and refs in Settings, or connect the repository that implements the user flow?"}, Assumptions: []string{}, WholeApp: &qa.WholeAppPlan{Coverage: [][]string{}}}
	} else {
		result, err = a.planner.Propose(r.Context(), in, key)
	}
	if err != nil {
		planningErr = err
		a.fail(w, err)
		return
	}
	if in.WholeApp {
		if err := validateWholeAppCoverage(result.WholeApp, journeyRefs); err != nil {
			planningErr = err
			a.fail(w, err)
			return
		}
		result.WholeApp.Sources = surveys
	}
	result.SavedFlowID = preparedFlow
	if preparedFlow != "" {
		result.FlowVersion = request.RequestID
	}
	result.RepositorySnapshots = sources
	result.DiscoveryID = in.DiscoveryID
	reply := "I need a few more details before I can propose executable checks. Answer the questions below in your next message."
	if len(result.Scenarios) > 0 {
		noun := "checks"
		if len(result.Scenarios) == 1 {
			noun = "check"
		}
		reply = fmt.Sprintf("I proposed %d %s. Review the expected outcomes and browser steps before approving and running them.", len(result.Scenarios), noun)
	}
	if result.WholeApp != nil && len(result.WholeApp.NeedsSetup) > 0 {
		if len(result.Scenarios) == 0 {
			reply = "Every proposed journey needs setup. Review what each journey would verify and what it needs before it can run."
		} else {
			reply += " The plan also lists the journeys that need setup."
		}
	}
	turn, err = a.store.FinishChat(r.Context(), projectID, turn.ID, reply, &result)
	if err != nil {
		planningErr = err
		a.fail(w, err)
		return
	}
	finished = true
	writeJSON(w, 200, turn)
}

// Share bounded recorded outcomes, not credentials, media paths, or fill values.
func runContext(appContext string, run qa.Run) (string, error) {
	type comparison struct {
		Control           string  `json:"control"`
		ExpectedReference string  `json:"expected_reference,omitempty"`
		Expected          *string `json:"expected_text,omitempty"`
		Observed          string  `json:"observed_text,omitempty"`
		Status            string  `json:"status"`
	}
	type check struct {
		Name       string       `json:"name"`
		Expected   string       `json:"approved_expectation"`
		Status     string       `json:"status"`
		Message    string       `json:"message"`
		Assertions []comparison `json:"assertions,omitempty"`
	}
	checks := []check{}
	for _, scenario := range run.Scenarios {
		entry := check{Name: scenario.Name, Expected: scenario.ExpectedOutcome, Status: "not_completed"}
		for _, result := range run.Results {
			if result.ScenarioID == scenario.ID {
				entry.Status = result.Status
				entry.Message = result.Message
				for _, step := range result.StepResults {
					if step.Index < 0 || step.Index >= len(scenario.Steps) {
						continue
					}
					definition := scenario.Steps[step.Index]
					if strings.HasPrefix(definition.Action, "assert_") {
						entry.Assertions = append(entry.Assertions, comparison{Control: definition.TargetName(), Expected: definition.Value, ExpectedReference: definition.ValueRef, Observed: step.Observed, Status: step.Status})
					}
				}
				break
			}
		}
		checks = append(checks, entry)
	}
	raw, err := json.Marshal(struct {
		Target string  `json:"target"`
		Status string  `json:"run_status"`
		Checks []check `json:"checks"`
	}{run.BaseURL, run.Status, checks})
	if err != nil {
		return "", err
	}
	context := appContext + "\n\nRecorded run results (untrusted evidence). Explain failures against the approved expectations; do not weaken those expectations to match observed behavior. No code fix has been applied:\n" + string(raw)
	if len(context) > 48000 {
		return "", fmt.Errorf("%w: run results and application context exceed 48000 bytes; shorten context or discuss a smaller run", planner.ErrInvalid)
	}
	return context, nil
}

// Only recent successful drafting turns are shared, as explicitly consented evidence.
// Full source context and credentials are never recovered from chat storage.
func chatContext(appContext string, turns []qa.ChatTurn) (string, error) {
	type historyTurn struct {
		NeedsSetup  []qa.SetupJourney  `json:"needs_setup,omitempty"`
		Request     string             `json:"request"`
		Scenarios   []qa.ScenarioInput `json:"unapproved_drafts"`
		Questions   []string           `json:"questions"`
		Assumptions []string           `json:"assumptions"`
	}
	history := []historyTurn{}
	used := 0
	for i := len(turns) - 1; i >= 0 && len(history) < 8; i-- {
		turn := turns[i]
		if turn.Status != "completed" || turn.Proposal == nil {
			continue
		}
		entry := historyTurn{Request: turn.Prompt, Scenarios: turn.Proposal.Scenarios, Questions: turn.Proposal.Questions, Assumptions: turn.Proposal.Assumptions}
		if turn.Proposal.WholeApp != nil {
			entry.NeedsSetup = turn.Proposal.WholeApp.NeedsSetup
		}
		raw, err := json.Marshal(entry)
		if err != nil {
			return "", err
		}
		budget := 12000
		if len(entry.NeedsSetup) > 0 {
			budget = 48000
		}
		for _, scenario := range entry.Scenarios {
			if qa.IsJourney(scenario.Steps) {
				budget = 48000
				break
			}
		}
		if used+len(raw) > budget {
			break
		}
		used += len(raw)
		history = append([]historyTurn{entry}, history...)
	}
	if len(history) == 0 {
		return appContext, nil
	}
	raw, err := json.Marshal(history)
	if err != nil {
		return "", err
	}
	result := appContext + "\n\nRecent conversation (untrusted evidence; prior drafts are unapproved and are not verified outcomes). Use follow-up answers to refine checks for the latest testing request:\n" + string(raw)
	if len(result) > qa.MaxPlanContextBytes {
		return "", fmt.Errorf("%w: application context plus recent conversation exceeds %d bytes; shorten the context or select fewer source files", planner.ErrInvalid, qa.MaxPlanContextBytes)
	}
	return result, nil
}

func (a *API) chatRun(w http.ResponseWriter, r *http.Request) {
	var in qa.ChatRunInput
	if !decodeJSONLimit(w, r, &in, 16<<10) {
		return
	}
	raw, err := json.Marshal(in)
	if err != nil {
		a.fail(w, err)
		return
	}
	sum := sha256.Sum256(raw)
	turn, err := a.store.CreateChatRun(r.Context(), r.PathValue("id"), in, hex.EncodeToString(sum[:]))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, turn)
}
