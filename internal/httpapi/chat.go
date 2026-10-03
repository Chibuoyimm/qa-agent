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
	page, err := a.store.ListChat(r.Context(), r.PathValue("id"), before)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, page)
}

type chatProposalInput struct {
	planner.Input
	RequestID string `json:"request_id"`
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
	in := request.Input
	// An empty first request is useful: the model must ask for requirements, not invent tests.
	if strings.TrimSpace(in.Context) == "" && len(in.RepositorySnapshotIDs) == 0 && in.DiscoveryID == "" {
		in.Context = "No application context supplied. Ask for missing business expectations, paths and test IDs before proposing executable checks."
	}
	key := r.Header.Get("X-QA-Provider-Key")
	in.WorkspaceID = r.Header.Get("X-QA-Anthropic-Workspace")
	projectID := r.PathValue("id")
	in, sources, err := a.prepareProposal(r.Context(), projectID, in, key)
	if err != nil {
		a.fail(w, err)
		return
	}
	turn, fresh, err := a.store.BeginChat(r.Context(), projectID, request.RequestID, request.Prompt, fingerprint)
	if err != nil {
		a.fail(w, err)
		return
	}
	if !fresh {
		writeJSON(w, 200, turn)
		return
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		// Cancellation must still close the persisted pending turn; no inference is retried.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()
		if _, err := a.store.FinishChat(ctx, projectID, request.RequestID, "This request was interrupted or failed. No checks were run. Send a new message to try again.", nil); err != nil {
			a.logger.Error("close interrupted chat request", "error", err)
		}
	}()
	page, err := a.store.ListChat(r.Context(), projectID, 0)
	if err != nil {
		a.fail(w, err)
		return
	}
	in.Context, err = chatContext(in.Context, page.Turns)
	if err != nil {
		a.fail(w, err)
		return
	}
	if err := a.planner.Validate(in, key); err != nil {
		a.fail(w, err)
		return
	}
	result, err := a.planner.Propose(r.Context(), in, key)
	if err != nil {
		a.fail(w, err)
		return
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
	turn, err = a.store.FinishChat(r.Context(), projectID, turn.ID, reply, &result)
	if err != nil {
		a.fail(w, err)
		return
	}
	finished = true
	writeJSON(w, 200, turn)
}

// Only recent successful drafting turns are shared, as explicitly consented evidence.
// Full source context and credentials are never recovered from chat storage.
func chatContext(appContext string, turns []qa.ChatTurn) (string, error) {
	type historyTurn struct {
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
		entry := historyTurn{turn.Prompt, turn.Proposal.Scenarios, turn.Proposal.Questions, turn.Proposal.Assumptions}
		raw, err := json.Marshal(entry)
		if err != nil {
			return "", err
		}
		if used+len(raw) > 12000 {
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
	if len(result) > 60000 {
		return "", fmt.Errorf("%w: application context plus recent conversation exceeds 60000 bytes; shorten application context", planner.ErrInvalid)
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
