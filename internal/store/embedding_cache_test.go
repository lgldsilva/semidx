package store

import (
	"context"
	"testing"
)

// Regression: only the first queued statement's result was read and the
// batch Close error was discarded, so later failures were silently lost.
func TestInsertEmbeddingCacheReportsLaterFailures(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.EnsureChunksTable(ctx, 3); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}
	err := s.InsertEmbeddingCache(ctx, []string{"ok-1", "bad-2"}, "m",
		[][]float32{{1, 0, 0}, {1, 0}}, 3)
	if err == nil {
		t.Fatal("InsertEmbeddingCache with a wrong-dimension second row must fail")
	}
}
