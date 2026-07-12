package localstore

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestEnsureSchemaMigratesMissingLastIndexedCommit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "old.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// Pre-migration schema: projects without last_indexed_commit.
	if _, err := db.Exec(`
		CREATE TABLE projects (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			path TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'registered',
			source_type TEXT NOT NULL DEFAULT '',
			git_url TEXT NOT NULL DEFAULT '',
			branch TEXT NOT NULL DEFAULT '',
			identity TEXT NOT NULL DEFAULT '',
			dims INTEGER NOT NULL DEFAULT 0,
			license_spdx_id TEXT NOT NULL DEFAULT ''
		);
	`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("New old db: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	ctx := context.Background()
	pid, err := s.UpsertProject(ctx, "migrated", "/tmp/m", "bge-m3", 3)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if err := s.UpdateProjectCommit(ctx, pid, "abc"); err != nil {
		t.Fatalf("UpdateProjectCommit: %v", err)
	}
	if sha, err := s.GetProjectCommit(ctx, pid); err != nil || sha != "abc" {
		t.Fatalf("commit = %q err=%v", sha, err)
	}
}

func TestEnsureSchemaMigratesMissingIdentity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE projects (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			path TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT ''
		);
	`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("New legacy db: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	ctx := context.Background()
	if _, err := s.UpsertProject(ctx, "fresh", "/tmp/f", "bge-m3", 3); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
}
