package qa

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Progress cannot change the approved snapshot, run outcome, gate or lease expiry.
// A cancelled worker may only finish reporting its already-approved cleanup.
func (s *Store) RecordProgress(ctx context.Context, id string, in CompleteInput) error {
	if in.LeaseToken == "" || len(in.Results) == 0 {
		return ErrInvalid
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status, token string
	var raw []byte
	var valid bool
	err = tx.QueryRow(ctx, `SELECT status,COALESCE(lease_token,''),scenarios,COALESCE(lease_expires_at>clock_timestamp(),false) OR (status IN ('cancelled','error') AND finished_at>clock_timestamp()-interval '2 minutes') FROM runs WHERE id=$1 FOR UPDATE`, id).Scan(&status, &token, &raw, &valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(in.LeaseToken))
	if token != hex.EncodeToString(hash[:]) || !valid || (status != "running" && status != "cancelled" && status != "error") {
		return ErrConflict
	}
	var scenarios []Scenario
	if err = json.Unmarshal(raw, &scenarios); err != nil {
		return err
	}
	if len(in.Results) > len(scenarios) {
		return ErrInvalid
	}
	for i, result := range in.Results {
		if result.ScenarioID != scenarios[i].ID || (result.Status == "running" && i != len(in.Results)-1) {
			return ErrInvalid
		}
	}
	if _, err = validateResults(scenarios, in.Results, true); err != nil {
		return err
	}
	if status != "running" {
		last := in.Results[len(in.Results)-1]
		if len(last.StepResults) == 0 || last.Status == "running" || last.StepResults[len(last.StepResults)-1].Action != "cleanup_fixture" {
			return fmt.Errorf("%w: only final cleanup can be reported after cancellation", ErrConflict)
		}
	}
	encoded, err := json.Marshal(in.Results)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE runs SET results=$2 WHERE id=$1`, id, encoded)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
