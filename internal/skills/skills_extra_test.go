package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallErrorPaths verifies Install returns errors for write failures.
func TestInstallErrorPaths(t *testing.T) {
	t.Parallel()

	// Create a file where Install expects to write a directory. Install will
	// try to create a subdirectory and fail because a file exists there.
	// This works regardless of uid/permissions.
	filedir := t.TempDir()
	fpath := filepath.Join(filedir, "semidx") // Install creates subdirs under this
	if err := os.WriteFile(fpath, []byte("block"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := Install(fpath); err == nil {
		t.Error("Install where target is a file should error")
	}

	// Also test with a read-only parent directory (best-effort, may not work as root).
	readonly := t.TempDir()
	if err := os.Chmod(readonly, 0o500); err != nil {
		t.Skipf("cannot chmod: %v", err)
	}
	defer func() { _ = os.Chmod(readonly, 0o700) }()

	if _, err := Install(readonly); err == nil {
		t.Skip("Install into read-only dir did not error (running as root?)")
	}
}

// TestInstallIncludesAllEmbeddedSkills verifies all embedded skill dirs
// are actually written to disk by Install.
func TestInstallIncludesAllEmbeddedSkills(t *testing.T) {
	t.Parallel()
	names, err := Names()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("Names returned empty list")
	}

	dir := t.TempDir()
	written, err := Install(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Every skill name should have at least one file written under it.
	for _, name := range names {
		found := false
		for _, p := range written {
			if strings.Contains(p, name) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("skill %q has no written files: %v", name, written)
		}
	}

	// Verify each listed directory actually exists on disk.
	for _, name := range names {
		d := filepath.Join(dir, name)
		info, err := os.Stat(d)
		if err != nil {
			t.Errorf("stat skill dir %s: %v", d, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", d)
		}
	}
}

// TestInstallCreatesParentDirectories verifies that Install creates the
// parent directory of the install dir when it does not exist.
func TestInstallCreatesParentDirectories(t *testing.T) {
	t.Parallel()
	deep := filepath.Join(t.TempDir(), "a", "b", "skills")
	written, err := Install(deep)
	if err != nil {
		t.Fatalf("Install(deep): %v", err)
	}
	if len(written) == 0 {
		t.Fatal("no files written")
	}
	// Verify the deepest file exists.
	info, err := os.Stat(written[0])
	if err != nil {
		t.Fatalf("stat %s: %v", written[0], err)
	}
	if info.Size() == 0 {
		t.Errorf("file %s is empty", written[0])
	}
}
