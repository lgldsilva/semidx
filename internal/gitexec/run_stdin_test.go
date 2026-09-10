package gitexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/gitenv"
)

func TestRunStdinCheckIgnore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	runCmd := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(gitenv.Clean(os.Environ()), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	runCmd("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := RunStdin(context.Background(), dir,
		"ignored.txt\x00kept.txt\x00", "check-ignore", "-z", "--stdin")
	if err != nil {
		t.Fatalf("RunStdin check-ignore: %v", err)
	}
	if !strings.Contains(out, "ignored.txt") {
		t.Errorf("output = %q, want ignored.txt", out)
	}
	if strings.Contains(out, "kept.txt") {
		t.Errorf("output = %q, kept.txt should not be reported as ignored", out)
	}
}

func TestRunStdinOutsideRepositoryFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := RunStdin(context.Background(), dir,
		"a\x00", "check-ignore", "-z", "--stdin"); err == nil {
		t.Error("expected error outside a git repository")
	}
}
