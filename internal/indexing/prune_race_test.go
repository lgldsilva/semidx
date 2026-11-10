package indexing

import (
	"context"
	"testing"
)

// TestConcurrentPruneKeepsInFlightVersion is a regression test for the prune
// race: worktree A stores a new version but writes its manifest only at the
// end of the run, so a prune from another worktree (or the watcher's
// removeFile) in between reclaimed A's not-yet-referenced row.
func TestConcurrentPruneKeepsInFlightVersion(t *testing.T) {
	ctx := context.Background()
	st, pid := newWorktreeTestStore(t)
	content := []byte("package a\n\nfunc InFlight() {}\n")

	idx := worktreeIndexer(st, &fakeEmbedder{})
	if _, err := idx.IndexContent(ctx, pid, "a.go", "m", content); err != nil {
		t.Fatalf("IndexContent: %v", err)
	}
	// Another worktree finishes meanwhile and prunes.
	if err := st.SetWorktreeFiles(ctx, pid, "/wt/B", map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PruneUnreferencedFiles(ctx, pid); err != nil {
		t.Fatal(err)
	}
	upToDate, err := st.FileUpToDate(ctx, pid, "a.go", ContentHash(content), 3)
	if err != nil || !upToDate {
		t.Fatalf("in-flight version pruned by a concurrent prune: upToDate=%v err=%v", upToDate, err)
	}

	// Finalizing the run must clear the pending references.
	idx.recordWorktree(ctx, pid, map[string]string{"a.go": ContentHash(content)}, nil)
	pending, err := st.WorktreeFiles(ctx, pid, pendingWorktree("/wt/A"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending references not cleared: %v", pending)
	}
}
