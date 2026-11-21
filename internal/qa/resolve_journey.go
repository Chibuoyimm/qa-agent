package qa

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

// A named request must resolve within this project. A pronoun may use a saved
// selection from this chat, or the task's own journey on a new conversation.
func (s *Store) SetupJourney(ctx context.Context, project string, task TestingTask, id, reference string) (string, string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback(ctx)
	var currentName string
	if err := tx.QueryRow(ctx, "SELECT name FROM journeys WHERE project_id=$1 AND id=$2", project, task.JourneyID).Scan(&currentName); err != nil {
		return "", "", err
	}
	if id == "" && reference == "" {
		err := tx.QueryRow(ctx, `SELECT COALESCE(NULLIF(saved_journey_id,''),proposal->>'saved_flow_id') FROM chat_turns WHERE project_id=$1 AND task_id=$2 AND status='completed' AND COALESCE(NULLIF(saved_journey_id,''),proposal->>'saved_flow_id','')<>'' ORDER BY sequence DESC LIMIT 1`, project, task.ID).Scan(&id)
		if err != nil && err != pgx.ErrNoRows {
			return "", "", err
		}
		if id == "" {
			id = task.JourneyID
		}
	}
	items, err := savedCandidates(ctx, tx, project)
	if err != nil {
		return "", "", err
	}
	if !slices.ContainsFunc(items, func(c journeyCandidate) bool { return c.Item == task.JourneyID }) {
		items = append(items, journeyCandidate{journeyChoice: journeyChoice{Item: task.JourneyID, Name: currentName}})
	}
	if id != "" {
		if slices.ContainsFunc(items, func(c journeyCandidate) bool { return c.Item == id }) {
			return id, "", nil
		}
		return "", "", ErrNotFound
	}
	matches := matchNamedJourneys(items, reference)
	if len(matches) == 1 {
		return matches[0].Item, "", nil
	}
	if len(matches) == 0 {
		matches = items
	}
	var names []string
	for _, c := range matches {
		names = append(names, c.Name)
	}
	return "", "Which journey should I resolve and run? Use its name: " + strings.Join(names, ", ") + ".", nil
}

func (s *Store) UpdateSetupProgress(ctx context.Context, project, id, message string) error {
	result, err := s.db.Exec(ctx, "UPDATE chat_turns SET reply=$3 WHERE project_id=$1 AND id=$2 AND status='pending'", project, id, message)
	if err == nil && result.RowsAffected() != 1 {
		return ErrConflict
	}
	return err
}

func (s *Store) FinishResolvedRun(ctx context.Context, project, task, id, target string, p Proposal) (ChatTurn, error) {
	var empty ChatTurn
	if len(p.Scenarios) != 1 || len(p.Questions) != 0 || p.SavedFlowID == "" {
		return empty, ErrInvalid
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	var current string
	if err = tx.QueryRow(ctx, "SELECT base_url FROM projects WHERE id=$1 FOR UPDATE", project).Scan(&current); err != nil {
		return empty, err
	}
	if current != target {
		return empty, ErrConflict
	}
	runID, err := newID()
	if err != nil {
		return empty, err
	}
	tag, err := tx.Exec(ctx, `UPDATE chat_turns SET status='completed',reply=$4,proposal=$5,saved_journey_id=$6 WHERE project_id=$1 AND task_id=$2 AND id=$3 AND status='pending'`, project, task, id, "I resolved the setup and queued this journey. Open the run to follow its progress and see the results.", p, p.SavedFlowID)
	if err != nil {
		return empty, err
	}
	if tag.RowsAffected() != 1 {
		return empty, ErrConflict
	}
	// A newly tracked journey gets its first reusable plan. Existing saved
	// requirements keep their original provenance and revision history.
	first := JourneyPlan{TaskID: task, TurnID: id, Item: "ready:0", ExpectedOutcome: p.Scenarios[0].ExpectedOutcome, Steps: p.Scenarios[0].Steps}
	if _, err = tx.Exec(ctx, "UPDATE journeys SET saved_plan=$1 WHERE project_id=$2 AND id=$3 AND saved_plan IS NULL", first, project, p.SavedFlowID); err != nil {
		return empty, err
	}
	approval, err := s.approveTaskTx(ctx, tx, project, task, TaskApprovalInput{BaseURL: target, TurnID: id, Drafts: []ReviewedDraft{{Index: 0, ScenarioInput: p.Scenarios[0]}}})
	if err != nil {
		return empty, err
	}
	checks, err := approvedRunScenarios(ctx, tx, project, approval.ScenarioIDs)
	if err != nil {
		return empty, err
	}
	if _, err = insertRunSnapshot(ctx, tx, runID, project, target, "advisory", task, checks); err != nil {
		return empty, err
	}
	turn, err := scanChatTurn(tx.QueryRow(ctx, `UPDATE chat_turns SET run_id=$1 WHERE id=$2 RETURNING `+chatColumns, runID, id))
	if err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, fmt.Errorf("queue resolved journey: %w", err)
	}
	return turn, nil
}
