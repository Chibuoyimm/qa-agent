package migrations

import (
	"context"
	"embed"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed 001_init.sql 002_repository_snapshots.sql 003_discoveries.sql 004_releases.sql 005_chat.sql 006_repository_providers.sql 007_testing_workspace.sql 008_verified_access.sql 009_planning_failures.sql 010_access_verification.sql 011_repository_surveys.sql 012_saved_journeys.sql
var files embed.FS

// Apply creates the pilot schema on server startup. The migration is idempotent.
func Apply(ctx context.Context, db *pgxpool.Pool) error {
	for _, name := range []string{"001_init.sql", "002_repository_snapshots.sql", "003_discoveries.sql", "004_releases.sql", "005_chat.sql", "006_repository_providers.sql", "007_testing_workspace.sql", "008_verified_access.sql", "009_planning_failures.sql", "010_access_verification.sql", "011_repository_surveys.sql", "012_saved_journeys.sql"} {
		schema, err := files.ReadFile(name)
		if err != nil {
			return err
		}
		if _, err := db.Exec(ctx, string(schema)); err != nil {
			return err
		}
	}
	return nil
}
