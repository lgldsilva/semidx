package repotools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// setupTestRepo creates a real git repository in a temp directory with two
// branches (main and feat/test) and a worktree (wt-feat). Returns the repo root
// and registers cleanup for the external worktree.
func setupTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}

	run("init", "--initial-branch=main")
	for _, cfg := range []struct{ k, v string }{
		{"user.name", "test"},
		{"user.email", "test@test"},
	} {
		run("config", cfg.k, cfg.v)
	}

	// First commit on main.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "chore: initial")

	// Create a second branch with a commit.
	run("checkout", "-b", "feat/test")
	if err := os.WriteFile(filepath.Join(dir, "FEATURE.md"), []byte("# feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "feat: add feature")

	// Back to main for a predictable current-branch.
	run("checkout", "main")

	// Create a worktree. Use an absolute path to avoid ".." issues.
	wtDir := filepath.Join(filepath.Dir(dir), "wt-feat")
	run("worktree", "add", wtDir)
	t.Cleanup(func() {
		_ = exec.Command("git", "worktree", "remove", "--force", wtDir).Run()
	})

	return dir
}

// ---------------------------------------------------------------------------
// Integration tests (real git repo)
// ---------------------------------------------------------------------------

func TestListWorktrees(t *testing.T) {
	dir := setupTestRepo(t)

	ctx := context.Background()
	wts, err := ListWorktrees(ctx, dir)
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}

	if len(wts) < 2 {
		t.Fatalf("expected at least 2 worktrees, got %d: %+v", len(wts), wts)
	}

	// First worktree should be the main repo.
	if wts[0].Path != dir {
		t.Errorf("first worktree path = %q, want %q", wts[0].Path, dir)
	}
	if len(wts[0].HEAD) != 7 {
		t.Errorf("first worktree HEAD = %q, want 7-char SHA", wts[0].HEAD)
	}
	if wts[0].Branch != "main" {
		t.Errorf("first worktree branch = %q, want %q", wts[0].Branch, "main")
	}

	// Second worktree should be wt-feat.
	expectedWTPath := filepath.Join(filepath.Dir(dir), "wt-feat")
	if wts[1].Path != expectedWTPath {
		t.Errorf("second worktree path = %q, want %q", wts[1].Path, expectedWTPath)
	}
	if len(wts[1].HEAD) != 7 {
		t.Errorf("second worktree HEAD = %q, want 7-char SHA", wts[1].HEAD)
	}
}

func TestListBranches(t *testing.T) {
	dir := setupTestRepo(t)

	ctx := context.Background()
	branches, err := ListBranches(ctx, dir, false)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}

	if len(branches) < 2 {
		t.Fatalf("expected at least 2 branches, got %d: %+v", len(branches), branches)
	}

	// Both branches should be local.
	for _, b := range branches {
		if b.Remote {
			t.Errorf("unexpected remote branch %q with only local query", b.Name)
		}
	}

	// Should have "feat/test" (sorted after "main").
	if branches[0].Name != "feat/test" {
		t.Errorf("first branch = %q, want %q", branches[0].Name, "feat/test")
	}
	if branches[0].FullRef != "refs/heads/feat/test" {
		t.Errorf("first branch FullRef = %q, want %q", branches[0].FullRef, "refs/heads/feat/test")
	}

	if branches[1].Name != "main" {
		t.Errorf("second branch = %q, want %q", branches[1].Name, "main")
	}

	// main should be the current branch.
	found := false
	for _, b := range branches {
		if b.Name == "main" && b.Current {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'main' to be marked as Current")
	}
}

func TestListBranches_includeRemote(t *testing.T) {
	dir := setupTestRepo(t)
	ctx := context.Background()

	// Repo has no remotes, so remotes should be empty.
	branches, err := ListBranches(ctx, dir, true)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}

	for _, b := range branches {
		if b.Remote {
			t.Errorf("unexpected remote branch %q in repo with no remotes", b.Name)
		}
	}
}

func TestStatus(t *testing.T) {
	dir := setupTestRepo(t)
	ctx := context.Background()

	// Initial state on main should be clean.
	status, err := Status(ctx, dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if status.Dirty {
		t.Error("expected clean status after setup")
	}
	if status.Detached {
		t.Error("expected non-detached HEAD on main")
	}
	if status.CurrentBranch != "main" {
		t.Errorf("CurrentBranch = %q, want %q", status.CurrentBranch, "main")
	}
	if len(status.HEAD) != 7 {
		t.Errorf("HEAD = %q, want 7-char SHA", status.HEAD)
	}
}

func TestStatus_dirty(t *testing.T) {
	dir := setupTestRepo(t)
	ctx := context.Background()

	// Dirty the working tree.
	if err := os.WriteFile(filepath.Join(dir, "UNTRACKED.md"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}

	status, err := Status(ctx, dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if !status.Dirty {
		t.Error("expected dirty status after adding untracked file")
	}
}

func TestStatus_detached(t *testing.T) {
	dir := setupTestRepo(t)
	ctx := context.Background()

	// Detach HEAD at a specific commit.
	cmd := exec.Command("git", "checkout", "--detach", "HEAD")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout --detach: %v\n%s", err, out)
	}

	status, err := Status(ctx, dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if !status.Detached {
		t.Error("expected detached HEAD after checkout --detach")
	}
	if status.CurrentBranch != "" {
		t.Errorf("CurrentBranch = %q, want empty for detached HEAD", status.CurrentBranch)
	}
}

// ---------------------------------------------------------------------------
// Parser-only tests (no git repo needed)
// ---------------------------------------------------------------------------

func TestParseWorktreePorcelain(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []Worktree
	}{
		{
			name: "two worktrees with trailing blank",
			input: []string{
				"worktree /repo",
				"HEAD abc1234abc1234abc1234abc1234abc1234abc1",
				"branch refs/heads/main",
				"",
				"worktree /wt",
				"HEAD deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
				"branch refs/heads/wt-feat",
				"",
			},
			want: []Worktree{
				{Path: "/repo", HEAD: "abc1234", Branch: "main"},
				{Path: "/wt", HEAD: "deadbee", Branch: "wt-feat"},
			},
		},
		{
			name: "no trailing blank",
			input: []string{
				"worktree /repo",
				"HEAD abc1234abc1234abc1234abc1234abc1234abc1",
				"branch refs/heads/main",
			},
			want: []Worktree{
				{Path: "/repo", HEAD: "abc1234", Branch: "main"},
			},
		},
		{
			name: "detached HEAD (no branch)",
			input: []string{
				"worktree /repo",
				"HEAD abc1234abc1234abc1234abc1234abc1234abc1",
			},
			want: []Worktree{
				{Path: "/repo", HEAD: "abc1234"},
			},
		},
		{
			name: "bare worktree",
			input: []string{
				"worktree /bare.git",
				"bare",
			},
			want: []Worktree{
				{Path: "/bare.git", Bare: true},
			},
		},
		{
			name:  "empty input",
			input: []string{},
			want:  nil,
		},
		{
			name:  "blank lines only",
			input: []string{"", "", ""},
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseWorktreePorcelain(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d\n got: %+v\nwant: %+v", len(got), len(tt.want), got, tt.want)
			}
			for i := range tt.want {
				if got[i].Path != tt.want[i].Path ||
					got[i].HEAD != tt.want[i].HEAD ||
					got[i].Branch != tt.want[i].Branch ||
					got[i].Bare != tt.want[i].Bare {
					t.Errorf("index %d:\n got: %+v\nwant: %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseForEachRef(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []Branch
	}{
		{
			name: "local branches",
			input: []string{
				"refs/heads/main\torigin/main\t[ahead 1, behind 0]",
				"refs/heads/feat/test\t\t",
			},
			want: []Branch{
				{
					Name: "main", FullRef: "refs/heads/main",
					Tracking: "origin/main", Ahead: 1, Behind: 0,
				},
				{
					Name: "feat/test", FullRef: "refs/heads/feat/test",
				},
			},
		},
		{
			name: "including remote branch",
			input: []string{
				"refs/heads/main\torigin/main\t[ahead 1]",
				"refs/remotes/origin/main\t\t",
			},
			want: []Branch{
				{
					Name: "main", FullRef: "refs/heads/main",
					Tracking: "origin/main", Ahead: 1,
				},
				{
					Name: "origin/main", FullRef: "refs/remotes/origin/main",
					Remote: true,
				},
			},
		},
		{
			name: "behind only and gone",
			input: []string{
				"refs/heads/main\torigin/main\t[behind 2]",
				"refs/heads/feature\torigin/feature\t[gone]",
			},
			want: []Branch{
				{
					Name: "main", FullRef: "refs/heads/main",
					Tracking: "origin/main", Behind: 2,
				},
				{
					Name: "feature", FullRef: "refs/heads/feature",
					Tracking: "origin/feature",
				},
			},
		},
		{
			name:  "empty input",
			input: []string{},
			want:  nil,
		},
		{
			name:  "blank lines",
			input: []string{"", ""},
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseForEachRef(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d\n got: %+v\nwant: %+v", len(got), len(tt.want), got, tt.want)
			}
			for i := range tt.want {
				w := tt.want[i]
				g := got[i]
				if g.Name != w.Name || g.FullRef != w.FullRef ||
					g.Remote != w.Remote || g.Current != w.Current ||
					g.Tracking != w.Tracking ||
					g.Ahead != w.Ahead || g.Behind != w.Behind {
					t.Errorf("index %d:\n got: %+v\nwant: %+v", i, g, w)
				}
			}
		})
	}
}
