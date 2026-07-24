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
	// semidxLine matches an invocation line in a skill's code fences. Requiring
	// semidx at line start (after an optional shell prompt) keeps prose comments
	// like "# semidx handles this" from looking like commands.
	semidxLine = regexp.MustCompile(`(?m)^[\$>\s]*(?:sudo\s+)?semidx\s+([^\n` + "`" + `]+)`)
	// cmdWord is a cobra subcommand token (lowercase word); URLs/flags/values fail it.
	cmdWord = regexp.MustCompile(`^[a-z][a-z-]*$`)
)

// TestSkillCommandsExist is the F6 anti-drift guarantee: every `semidx <cmd>`
// cited in ANY shipped SKILL.md must resolve to a real command in the cobra
// tree. If someone renames or removes a command without updating the skills (or
// vice versa), this fails — a skill can never document a command that does not
// exist.
func TestSkillCommandsExist(t *testing.T) {
	dir := t.TempDir()
	if _, err := skills.Install(dir); err != nil {
		t.Fatal(err)
	}
	names, err := skills.Names()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("no embedded skills")
	}

	root := newRootCmd()
	totalPaths := 0
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name, "SKILL.md"))
		if err != nil {
			t.Fatalf("%s/SKILL.md: %v", name, err)
		}
		paths := citedCommandPaths(string(data))
		totalPaths += len(paths)
		for _, path := range paths {
			cmd, _, err := root.Find(path)
			if err != nil || cmd == nil || cmd.Name() != path[len(path)-1] {
				t.Errorf("%s cites `semidx %s`, which is not a real command", name, strings.Join(path, " "))
			}
		}
	}
	if totalPaths == 0 {
		t.Fatal("no semidx commands found in any SKILL.md — the extractor or skills are broken")
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
