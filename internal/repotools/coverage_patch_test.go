// coverage-patch: 2026-07-17
package repotools

import (
	"context"
	"testing"
)

func TestParseWorktreePorcelain_consecutiveWorktrees(t *testing.T) {
	// Two "worktree" lines with no blank between flushes via startWorktree.
	got := parseWorktreePorcelain([]string{
		"worktree /a",
		"HEAD aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"worktree /b",
		"HEAD bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"bare",
	})
	if len(got) != 2 {
		t.Fatalf("got %d worktrees: %+v", len(got), got)
	}
	if got[0].Path != "/a" || got[1].Path != "/b" || !got[1].Bare {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestBranchFromFullRef_default(t *testing.T) {
	b, ok := branchFromFullRef("refs/tags/v1")
	if !ok {
		t.Fatal("expected ok")
	}
	if b.Name != "refs/tags/v1" {
		t.Errorf("name = %q", b.Name)
	}
	// Contains "/" → Remote true in default branch
	if !b.Remote {
		t.Error("expected Remote true for path-like default ref")
	}

	b, ok = branchFromFullRef("weird")
	if !ok || b.Name != "weird" || b.Remote {
		t.Errorf("simple default: %+v ok=%v", b, ok)
	}

	// origin/HEAD filtered
	if _, ok := branchFromFullRef("refs/remotes/origin/HEAD"); ok {
		t.Error("origin/HEAD must be filtered")
	}
}

func TestListWorktrees_error(t *testing.T) {
	_, err := ListWorktrees(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("expected error on non-git dir")
	}
}

func TestListBranches_error(t *testing.T) {
	_, err := ListBranches(context.Background(), t.TempDir(), true)
	if err == nil {
		t.Fatal("expected error on non-git dir")
	}
}

func TestStatus_error(t *testing.T) {
	_, err := Status(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("expected error on non-git dir")
	}
}

func TestParseTrackingInfo_edge(t *testing.T) {
	var b Branch
	parseTrackingInfo(&b, "")
	parseTrackingInfo(&b, "[gone]")
	parseTrackingInfo(&b, "[ahead x, behind y]") // non-numeric ignored
	if b.Ahead != 0 || b.Behind != 0 {
		t.Errorf("invalid numbers should leave 0: %+v", b)
	}
	parseTrackingInfo(&b, "[ahead 3, behind 4]")
	if b.Ahead != 3 || b.Behind != 4 {
		t.Errorf("got ahead=%d behind=%d", b.Ahead, b.Behind)
	}
}
