package indexing

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/lgldsilva/semidx/internal/gitexec"
)

// gitIgnoredPaths returns the set of slash-separated relative paths that git
// ignores inside the repository containing projectPath — honouring .gitignore
// files at any level and .git/info/exclude. Any failure yields nil, meaning
// "keep every scanned path": scanning is unchanged for non-git projects,
// git-less machines, and repositories where nothing is ignored.
func gitIgnoredPaths(projectPath string, relPaths []string) map[string]bool {
	if len(relPaths) == 0 {
		return nil
	}
	paths := make([]string, len(relPaths))
	for i, rel := range relPaths {
		paths[i] = filepath.ToSlash(rel)
	}
	// check-ignore exits 1 when nothing matches — indistinguishable from "no
	// filtering needed", which is exactly the right fallback.
	out, err := gitexec.RunStdin(context.Background(), projectPath,
		strings.Join(paths, "\x00"), "check-ignore", "-z", "--stdin")
	if err != nil || out == "" {
		return nil
	}
	ignored := make(map[string]bool)
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			ignored[p] = true
		}
	}
	return ignored
}
