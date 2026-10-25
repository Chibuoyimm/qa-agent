package qa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type RecheckInput struct {
	RequestID string `json:"request_id"`
	BaseURL   string `json:"base_url"`
	Mode      string `json:"mode"`
}

func (s *Store) RecheckRun(ctx context.Context, sourceID string, in RecheckInput) (Run, error) {
	if err := ValidateChatRequest(in.RequestID, "Recheck approved outcomes"); err != nil {
		return Run{}, err
	}
	if in.Mode != "advisory" && in.Mode != "blocking" {
		return Run{}, ErrInvalid
	}
	source, err := s.GetRun(ctx, sourceID)
	if err != nil {
		return Run{}, err
	}
	if source.Status == "queued" || source.Status == "running" {
		return Run{}, fmt.Errorf("%w: wait for this run to finish before rechecking", ErrConflict)
	}
	target := in.BaseURL
	if target == "" {
		target = source.BaseURL
	}
	validated, err := ValidateProject(ProjectInput{Name: "Recheck", BaseURL: target}, s.allowed)
	if err != nil {
		return Run{}, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback(ctx)
	var projectID string
	if err := tx.QueryRow(ctx, `SELECT id FROM projects WHERE id=$1 FOR UPDATE`, source.ProjectID).Scan(&projectID); err != nil {
		return Run{}, err
	}
	previous, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id=$1 AND project_id=$2`, in.RequestID, source.ProjectID))
	if err == nil {
		if previous.SourceRunID != sourceID || previous.BaseURL != validated.BaseURL || previous.Mode != in.Mode {
			return Run{}, ErrConflict
		}
		return previous, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Run{}, err
	}
	// Copy the source snapshot verbatim, including its expected outcomes and steps.
	snapshot, err := json.Marshal(source.Scenarios)
	if err != nil {
		return Run{}, err
	}
	run, err := scanRun(tx.QueryRow(ctx, `INSERT INTO runs(id,project_id,base_url,mode,status,gate,scenarios,results,task_id,source_run_id) VALUES($1,$2,$3,$4,'queued','pending',$5,'[]'::jsonb,NULLIF($6,''),$7) RETURNING `+runColumns, in.RequestID, source.ProjectID, validated.BaseURL, in.Mode, snapshot, source.TaskID, source.ID))
	if err != nil {
		return Run{}, err
	}
	if source.TaskID != "" {
		turnID, err := newID()
		if err != nil {
			return Run{}, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO chat_turns(id,project_id,task_id,fingerprint,prompt,status,reply,run_id) VALUES($1,$2,$3,$4,$5,'completed',$6,$7)`, turnID, source.ProjectID, source.TaskID, in.RequestID, "Recheck the approved outcomes", fmt.Sprintf("Rechecking the same %d approved checks against %s. Expectations are unchanged.", len(source.Scenarios), validated.BaseURL), run.ID)
		if err != nil {
			return Run{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Run{}, err
	}
	return run, nil
}

type FindingReview struct {
	ScenarioID string    `json:"scenario_id"`
	Status     string    `json:"status"`
	Owner      string    `json:"owner"`
	Reason     string    `json:"reason"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (s *Store) ReviewFinding(ctx context.Context, runID string, in FindingReview) (FindingReview, error) {
	in.Owner = strings.TrimSpace(in.Owner)
	in.Reason = strings.TrimSpace(in.Reason)
	if (in.Status != "Needs review" && in.Status != "Confirmed issue" && in.Status != "Environment issue" && in.Status != "Works as intended") || len(in.Owner) > 200 || len(in.Reason) > 2000 || (in.Status == "Works as intended" && in.Reason == "") {
		return in, fmt.Errorf("%w: select a review status and explain behavior marked as intended", ErrInvalid)
	}
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return in, err
	}
	found := false
	for _, result := range run.Results {
		if result.ScenarioID == in.ScenarioID && result.Status != "passed" {
			found = true
		}
	}
	if !found {
		return in, ErrNotFound
	}
	err = s.db.QueryRow(ctx, `INSERT INTO finding_reviews(run_id,scenario_id,status,owner,reason) VALUES($1,$2,$3,$4,$5) ON CONFLICT(run_id,scenario_id) DO UPDATE SET status=$3,owner=$4,reason=$5,updated_at=clock_timestamp() RETURNING updated_at`, runID, in.ScenarioID, in.Status, in.Owner, in.Reason).Scan(&in.UpdatedAt)
	return in, err
}
func (s *Store) FindingReviews(ctx context.Context, runID string) ([]FindingReview, error) {
	if _, err := s.GetRun(ctx, runID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT scenario_id,status,owner,reason,updated_at FROM finding_reviews WHERE run_id=$1`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []FindingReview{}
	for rows.Next() {
		var review FindingReview
		if err := rows.Scan(&review.ScenarioID, &review.Status, &review.Owner, &review.Reason, &review.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, review)
	}
	return list, rows.Err()
}
