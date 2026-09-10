package indexing

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newGitRepo turns dir into a git repository, mirroring gitexec's hermetic
// config so global excludes cannot leak into the fixture.
func newGitRepo(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

func TestScanFilesSkipsGitignoredPaths(t *testing.T) {
	dir := t.TempDir()
	newGitRepo(t, dir)
	writeFile(t, dir, ".gitignore", ".slim/\n*.log\n")
	writeFile(t, dir, "keep.go", "package keep\n")
	writeFile(t, dir, "keep.txt", "hello\n")
	writeFile(t, dir, "usage.log", "log\n")
	writeFile(t, dir, ".slim/a.go", "package a\n")
	writeFile(t, dir, ".slim/sub/b.md", "b\n")

	files, err := ScanFiles(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(files, "\n")
	for _, want := range []string{"keep.go", "keep.txt"} {
		if !strings.Contains(joined, filepath.Join(dir, want)) {
			t.Errorf("ScanFiles missing %s, got %v", want, files)
		}
	}
	for _, banned := range []string{".slim", ".log"} {
		if strings.Contains(joined, banned) {
			t.Errorf("ScanFiles returned gitignored path containing %q: %v", banned, files)
		}
	}
}

func TestScanFilesKeepsPathsOutsideGitRepositories(t *testing.T) {
	dir := t.TempDir() // no git init: a .gitignore alone must not filter anything
	writeFile(t, dir, ".gitignore", "keep.txt\n")
	writeFile(t, dir, "keep.txt", "hello\n")

	files, err := ScanFiles(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(files, "\n"), filepath.Join(dir, "keep.txt")) {
		t.Errorf("ScanFiles dropped keep.txt outside a git repo, got %v", files)
	}
}
