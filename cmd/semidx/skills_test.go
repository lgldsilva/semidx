package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/skills"
)

var (
	// semidxLine matches an invocation line in the skill's code fences.
	semidxLine = regexp.MustCompile(`semidx\s+([^\n` + "`" + `]+)`)
	// cmdWord is a cobra subcommand token (lowercase word); URLs/flags/values fail it.
	cmdWord = regexp.MustCompile(`^[a-z][a-z-]*$`)
)

// TestSkillCommandsExist is the F6 anti-drift guarantee: every `semidx <cmd>`
// cited in the shipped SKILL.md must resolve to a real command in the cobra tree.
// If someone renames or removes a command without updating the skill (or vice
// versa), this fails — the skill can never document a command that doesn't exist.
func TestSkillCommandsExist(t *testing.T) {
	dir := t.TempDir()
	if _, err := skills.Install(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "semantic-search", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	paths := citedCommandPaths(string(data))
	if len(paths) == 0 {
		t.Fatal("no semidx commands found in SKILL.md — the extractor or the skill is broken")
	}
	for _, path := range paths {
		cmd, _, err := root.Find(path)
		if err != nil || cmd == nil || cmd.Name() != path[len(path)-1] {
			t.Errorf("SKILL.md cites `semidx %s`, which is not a real command", strings.Join(path, " "))
		}
	}
}

// citedCommandPaths extracts the command path (leading run of subcommand words)
// from each `semidx ...` invocation inside fenced code blocks, de-duplicated.
// Only code fences count — prose like "semidx is client-server" is not a command.
func citedCommandPaths(md string) [][]string {
	seen := map[string]bool{}
	var paths [][]string
	for _, m := range semidxLine.FindAllStringSubmatch(fencedCode(md), -1) {
		var path []string
		for _, tok := range strings.Fields(m[1]) {
			if !cmdWord.MatchString(tok) {
				break // stop at the first flag/URL/value
			}
			path = append(path, tok)
		}
		if len(path) == 0 {
			continue
		}
		key := strings.Join(path, " ")
		if !seen[key] {
			seen[key] = true
			paths = append(paths, path)
		}
	}
	return paths
}

// fencedCode returns only the content inside ``` fenced blocks, joined — so the
// command extractor never sees prose.
func fencedCode(md string) string {
	var b strings.Builder
	inFence := false
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
