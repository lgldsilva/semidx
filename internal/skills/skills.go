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
	"strings"
)

//go:embed data
var content embed.FS

// ManagedMarker identifies skill files owned by `semidx skills install`.
// Unmanaged same-name skills are not overwritten unless Force is set.
const ManagedMarker = "<!-- semidx-managed: skill -->"

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

// InstallOptions controls overwrite behaviour.
type InstallOptions struct {
	// Force overwrites skill files that exist but lack ManagedMarker.
	Force bool
}

// Install writes every embedded skill into dir (e.g. ~/.claude/skills), creating
// <dir>/<skill>/... and returning the paths written. Managed files are always
// refreshed; unmanaged same-name skills are skipped unless opts.Force.
func Install(dir string, opts ...InstallOptions) ([]string, error) {
	var o InstallOptions
	if len(opts) > 0 {
		o = opts[0]
	}
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
		body := ensureManagedMarker(string(b))
		if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
			return err
		}
		if existing, err := os.ReadFile(dest); err == nil { // #nosec G304 -- dest is under Install Options.Dir + skill rel path
			if !o.Force && !strings.Contains(string(existing), ManagedMarker) {
				// Leave user-owned skill alone.
				return nil
			}
		}
		if err := os.WriteFile(dest, []byte(body), 0o600); err != nil {
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

func ensureManagedMarker(body string) string {
	if strings.Contains(body, ManagedMarker) {
		return body
	}
	// Insert after YAML frontmatter when present.
	if strings.HasPrefix(body, "---\n") {
		rest := body[4:]
		if i := strings.Index(rest, "\n---\n"); i >= 0 {
			return "---\n" + rest[:i+5] + ManagedMarker + "\n" + rest[i+5:]
		}
	}
	return ManagedMarker + "\n" + body
}
