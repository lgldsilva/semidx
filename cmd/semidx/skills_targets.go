package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// skillsTarget describes one agent skills install destination.
type skillsTarget struct {
	ID   string
	Desc string
	// userLevel is false for project-relative targets (skipped by --all unless asked).
	userLevel bool
	path      func(home, configDir string) string
}

var skillsTargetAliases = map[string]string{
	"claude": "claude-code",
	"agy":    "antigravity",
}

// skillsTargets is the supported --target matrix for `semidx skills install`.
var skillsTargets = []skillsTarget{
	{ID: "claude-code", Desc: "~/.claude/skills", userLevel: true,
		path: func(home, _ string) string { return filepath.Join(home, ".claude", "skills") }},
	{ID: "cursor", Desc: "~/.cursor/skills", userLevel: true,
		path: func(home, _ string) string { return filepath.Join(home, ".cursor", "skills") }},
	{ID: "windsurf", Desc: "~/.codeium/windsurf/skills", userLevel: true,
		path: func(home, _ string) string { return filepath.Join(home, ".codeium", "windsurf", "skills") }},
	{ID: "codex", Desc: "~/.codex/skills", userLevel: true,
		path: func(home, _ string) string { return filepath.Join(home, ".codex", "skills") }},
	{ID: "opencode", Desc: "~/.config/opencode/skills", userLevel: true,
		path: func(_, configDir string) string { return filepath.Join(configDir, "opencode", "skills") }},
	{ID: "pi", Desc: "~/.pi/agent/skills", userLevel: true,
		path: func(home, _ string) string { return filepath.Join(home, ".pi", "agent", "skills") }},
	{ID: "kimi", Desc: "$KIMI_CODE_HOME/skills (default ~/.kimi-code/skills)", userLevel: true,
		path: func(home, _ string) string { return filepath.Join(kimiCodeHome(home), "skills") }},
	{ID: "crush", Desc: "~/.config/crush/skills", userLevel: true,
		path: func(_, configDir string) string { return filepath.Join(configDir, "crush", "skills") }},
	{ID: "mimo", Desc: "~/.config/mimocode/skills", userLevel: true,
		path: func(_, configDir string) string { return filepath.Join(configDir, "mimocode", "skills") }},
	{ID: "antigravity", Desc: "~/.gemini/config/skills", userLevel: true,
		path: func(home, _ string) string { return filepath.Join(home, ".gemini", "config", "skills") }},
	{ID: "project", Desc: "./.claude/skills (cwd)", userLevel: false,
		path: func(_, _ string) string { return filepath.Join(".claude", "skills") }},
}

func kimiCodeHome(userHome string) string {
	if v := strings.TrimSpace(os.Getenv("KIMI_CODE_HOME")); v != "" {
		return v
	}
	return filepath.Join(userHome, ".kimi-code")
}

func canonicalizeSkillsTarget(id string) string {
	if c, ok := skillsTargetAliases[id]; ok {
		return c
	}
	return id
}

func skillsTargetByID(id string) (skillsTarget, bool) {
	id = canonicalizeSkillsTarget(id)
	for _, t := range skillsTargets {
		if t.ID == id {
			return t, true
		}
	}
	return skillsTarget{}, false
}

// resolveSkillsDir maps a --target keyword (or an explicit --dir) to a skills
// directory. home/configDir are injectable for tests; empty uses os defaults.
func resolveSkillsDir(target, dir, home, configDir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	if configDir == "" {
		var err error
		configDir, err = os.UserConfigDir()
		if err != nil {
			// Fall back to ~/.config when UserConfigDir fails (rare).
			configDir = filepath.Join(home, ".config")
		}
	}
	t, ok := skillsTargetByID(target)
	if !ok {
		return "", fmt.Errorf("unknown --target %q (see `semidx skills install --help`)", target)
	}
	return t.path(home, configDir), nil
}

func skillsTargetList() string {
	var b strings.Builder
	ids := make([]string, len(skillsTargets))
	for i, t := range skillsTargets {
		ids[i] = t.ID
		fmt.Fprintf(&b, "  %-12s %s\n", t.ID, t.Desc)
	}
	_ = ids
	fmt.Fprintf(&b, "\nAliases: claude→claude-code, agy→antigravity\n")
	return b.String()
}

func userLevelSkillsTargets() []skillsTarget {
	out := make([]skillsTarget, 0, len(skillsTargets))
	for _, t := range skillsTargets {
		if t.userLevel {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
