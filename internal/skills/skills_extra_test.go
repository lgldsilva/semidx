package skills

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestInstallErrorPaths verifies Install returns errors for write failures.
func TestInstallErrorPaths(t *testing.T) {
	t.Parallel()

	// Root on Unix can always write regardless of permissions — skip.
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		t.Skip("skipping read-only dir test as root")
	}

	// Install into a read-only directory should fail on the first write.
	readonly := t.TempDir()
	if err := os.Chmod(readonly, 0o500); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(readonly, 0o700) })

	if _, err := Install(readonly); err == nil {
		t.Error("Install into read-only dir should error")
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
