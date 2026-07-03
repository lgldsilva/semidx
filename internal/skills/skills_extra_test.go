package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallReturnsErrorWhenDestUnusable covers the error-wrapping path: when
// the destination cannot hold the skill tree (here, dir is actually a file), the
// first MkdirAll fails and Install returns a wrapped error and no paths.
func TestInstallReturnsErrorWhenDestUnusable(t *testing.T) {
	tmp := t.TempDir()
	fileAsDir := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(fileAsDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	written, err := Install(fileAsDir)
	if err == nil {
		t.Fatal("Install into a path that is a file should error")
	}
	if !strings.Contains(err.Error(), "install skills") {
		t.Errorf("error not wrapped with context: %v", err)
	}
	if written != nil {
		t.Errorf("no paths should be reported on failure, got %v", written)
	}
}

// TestInstallWriteFileError covers the per-file write failure branch: the skill
// directory is created fine, but the destination file path is pre-occupied by a
// directory, so os.WriteFile fails and Install returns the error.
func TestInstallWriteFileError(t *testing.T) {
	dir := t.TempDir()
	// SKILL.md is known to exist under the semantic-search skill; occupy its
	// destination path with a directory so the file write cannot succeed.
	blocker := filepath.Join(dir, "semantic-search", "SKILL.md")
	if err := os.MkdirAll(blocker, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir); err == nil {
		t.Error("Install should fail when a destination file path is a directory")
	}
}

// TestNamesAndInstallAgree asserts every directory reported by Names is created
// by Install under the destination.
func TestNamesAndInstallAgree(t *testing.T) {
	names, err := Names()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("Names returned no skills")
	}
	dir := t.TempDir()
	if _, err := Install(dir); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if fi, err := os.Stat(filepath.Join(dir, n)); err != nil || !fi.IsDir() {
			t.Errorf("skill %q not installed as a directory (err=%v)", n, err)
		}
	}
}
