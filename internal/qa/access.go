package qa

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// VerifiedAccess records which successful browser observation was explicitly
// selected as this app's default access setup. It stores no credentials.
type VerifiedAccess struct {
	DiscoveryID     string     `json:"discovery_id"`
	SetupScenarioID string     `json:"setup_scenario_id"`
	VerifiedAt      *time.Time `json:"verified_at,omitempty"`
}

func (s *Store) GetVerifiedAccess(ctx context.Context, projectID string) (VerifiedAccess, error) {
	var value VerifiedAccess
	err := s.db.QueryRow(ctx, `SELECT d.id,COALESCE(d.setup_scenario->>'id',''),d.finished_at FROM verified_app_access a JOIN discoveries d ON d.id=a.discovery_id WHERE a.project_id=$1`, projectID).Scan(&value.DiscoveryID, &value.SetupScenarioID, &value.VerifiedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = s.GetProject(ctx, projectID)
		return value, err
	}
	return value, err
}

func (s *Store) SaveVerifiedAccess(ctx context.Context, projectID, discoveryID string) (VerifiedAccess, error) {
	// The scoped insert rejects pending, failed and foreign discoveries atomically.
	tag, err := s.db.Exec(ctx, `INSERT INTO verified_app_access(project_id,discovery_id)
 SELECT project_id,id FROM discoveries WHERE id=$2 AND project_id=$1 AND status='completed'
 AND jsonb_array_length(result->'pages')>0
 ON CONFLICT(project_id) DO UPDATE SET discovery_id=excluded.discovery_id`, projectID, discoveryID)
	if err != nil {
		return VerifiedAccess{}, err
	}
	if tag.RowsAffected() != 1 {
		return VerifiedAccess{}, fmt.Errorf("%w: choose a completed browser access verification for this app", ErrInvalid)
	}
	return s.GetVerifiedAccess(ctx, projectID)
}
