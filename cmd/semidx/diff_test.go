package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestGetChangedFilesAndFileAtRef is a regression test for the '--' placement
// bug: putting the revision range after '--' made git treat it as a pathspec,
// so getChangedFiles always returned nothing and getFileAtRef read nothing.
func TestGetChangedFilesAndFileAtRef(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	// --no-verify: the user's global commit-msg hook would otherwise reject
	// these throwaway messages as non-Conventional-Commits.
	git("commit", "-q", "--no-verify", "-m", "c1")
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "--no-verify", "-m", "c2")

	// Pass the temp repo dir explicitly. The test must NOT os.Chdir into it:
	// that mutates the whole process working directory and, combined with git
	// commands elsewhere, has corrupted the real worktree's branch.
	files, err := getChangedFiles(dir, "HEAD~1", "HEAD", false)
	if err != nil {
		t.Fatalf("getChangedFiles: %v", err)
	}
	found := false
	for _, f := range files {
		if f == "b.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected b.txt among changed files, got %v", files)
	}

	content, err := getFileAtRef(dir, "a.txt", "HEAD")
	if err != nil {
		t.Fatalf("getFileAtRef: %v", err)
	}
	if content != "first\n" {
		t.Fatalf("getFileAtRef = %q, want %q", content, "first\n")
	}
}
