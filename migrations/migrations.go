package migrations

import (
	"context"
	"embed"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed 001_init.sql 002_repository_snapshots.sql
var files embed.FS

// Apply creates the pilot schema on server startup. The migration is idempotent.
func Apply(ctx context.Context, db *pgxpool.Pool) error {
	for _, name := range []string{"001_init.sql", "002_repository_snapshots.sql"} {
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
