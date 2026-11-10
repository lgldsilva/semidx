package store

import (
	"context"
	"testing"
)

// Regression: the trigram backfill matched LIKE 'chunks_%' (underscore is a
// wildcard, any schema), indexing tables that are not chunk tables.
func TestApplyTrigramIndexesOnlyChunkTables(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS chunks_notes (content text)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(context.Background(), `DROP TABLE IF EXISTS chunks_notes`) })
	if err := s.applyTrigramIndexes(ctx); err != nil {
		t.Fatalf("applyTrigramIndexes: %v", err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE indexname = 'chunks_notes_content_trgm'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("trigram index created on a non-chunk table")
	}
}
