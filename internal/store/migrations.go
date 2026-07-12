package store

import (
	"context"
	"database/sql"
	"embed"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver for goose
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrate applies all pending goose migrations to the database at connString.
// It uses a short-lived database/sql connection (goose needs *sql.DB) separate
// from the pgxpool used for normal operations.
func migrate(ctx context.Context, connString string) error {
	db, err := sql.Open("pgx", connString)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.UpContext(ctx, db, "migrations")
}
