package migrations

import (
	"context"
	"embed"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed 001_init.sql
var files embed.FS

// Apply creates the pilot schema on server startup. The migration is idempotent.
func Apply(ctx context.Context, db *pgxpool.Pool) error {
	schema, err := files.ReadFile("001_init.sql")
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, string(schema))
	return err
}
