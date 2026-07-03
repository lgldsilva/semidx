// Package skills ships the agent skills that teach AI assistants how to use
// semidx. The skill files are embedded in the binary so `semidx skills install`
// works from a single self-contained executable.
package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed data
var content embed.FS

// Names lists the embedded skills (directory names under data/).
func Names() ([]string, error) {
	entries, err := content.ReadDir("data")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// Install writes every embedded skill into dir (e.g. ~/.claude/skills), creating
// <dir>/<skill>/... and returning the paths written. Existing files are
// overwritten so re-running picks up an upgraded binary's skills.
func Install(dir string) ([]string, error) {
	var written []string
	err := fs.WalkDir(content, "data", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "data" {
			return nil
		}
		// Strip the leading "data/" so files land directly under dir.
		rel := p[len("data/"):]
		dest := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o750)
		}
		b, err := content.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(dest, b, 0o600); err != nil {
			return err
		}
		written = append(written, dest)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("install skills: %w", err)
	}
	return written, nil
}
