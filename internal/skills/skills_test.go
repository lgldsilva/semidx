package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNamesIncludesSemanticSearch(t *testing.T) {
	names, err := Names()
	if err != nil {
		t.Fatal(err)
	}
	required := []string{"semantic-search", "auto-index", "workspace-agent"}
	for _, want := range required {
		found := false
		for _, n := range names {
			if n == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s not among skills: %v", want, names)
		}
	}
}

func TestInstallWritesSkillFile(t *testing.T) {
	dir := t.TempDir()
	written, err := Install(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) == 0 {
		t.Fatal("no files written")
	}
	skillMD := filepath.Join(dir, "semantic-search", "SKILL.md")
	data, err := os.ReadFile(skillMD)
	if err != nil {
		t.Fatalf("SKILL.md not installed: %v", err)
	}
	if !strings.HasPrefix(string(data), "---") || !strings.Contains(string(data), "name: semantic-search") {
		t.Errorf("SKILL.md missing frontmatter:\n%s", firstLines(string(data), 3))
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := Install(dir); err != nil {
		t.Fatal(err)
	}
	// A second install over the same dir must succeed (overwrite), not error.
	if _, err := Install(dir); err != nil {
		t.Fatalf("re-install failed: %v", err)
	}
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
