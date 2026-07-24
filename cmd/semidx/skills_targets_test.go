package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSkillsDirMatrix(t *testing.T) {
	home := "/home/u"
	cfg := "/home/u/.config"
	cases := map[string]string{
		"claude-code": filepath.Join(home, ".claude", "skills"),
		"claude":      filepath.Join(home, ".claude", "skills"),
		"cursor":      filepath.Join(home, ".cursor", "skills"),
		"windsurf":    filepath.Join(home, ".codeium", "windsurf", "skills"),
		"codex":       filepath.Join(home, ".codex", "skills"),
		"opencode":    filepath.Join(cfg, "opencode", "skills"),
		"pi":          filepath.Join(home, ".pi", "agent", "skills"),
		"kimi":        filepath.Join(home, ".kimi-code", "skills"),
		"crush":       filepath.Join(cfg, "crush", "skills"),
		"mimo":        filepath.Join(cfg, "mimocode", "skills"),
		"antigravity": filepath.Join(home, ".gemini", "config", "skills"),
		"agy":         filepath.Join(home, ".gemini", "config", "skills"),
		"project":     filepath.Join(".claude", "skills"),
	}
	for id, want := range cases {
		got, err := resolveSkillsDir(id, "", home, cfg)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if got != want {
			t.Errorf("%s: got %s want %s", id, got, want)
		}
	}
}

func TestResolveSkillsDirExplicit(t *testing.T) {
	got, err := resolveSkillsDir("claude-code", "/tmp/custom", "/h", "/c")
	if err != nil || got != "/tmp/custom" {
		t.Fatalf("got=%s err=%v", got, err)
	}
}

func TestResolveSkillsDirUnknown(t *testing.T) {
	if _, err := resolveSkillsDir("nope", "", "/h", "/c"); err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveSkillsDirKimiEnv(t *testing.T) {
	t.Setenv("KIMI_CODE_HOME", "/opt/kimi")
	got, err := resolveSkillsDir("kimi", "", "/h", "/c")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join("/opt/kimi", "skills") {
		t.Fatalf("got=%s", got)
	}
}

func TestSkillsTargetListMentionsAliases(t *testing.T) {
	list := skillsTargetList()
	for _, want := range []string{"claude-code", "kimi", "mimo", "agy→antigravity", "pi"} {
		if !strings.Contains(list, want) {
			t.Errorf("list missing %q:\n%s", want, list)
		}
	}
}

func TestUserLevelSkillsTargetsSkipProject(t *testing.T) {
	for _, tgt := range userLevelSkillsTargets() {
		if tgt.ID == "project" {
			t.Fatal("project must not be user-level")
		}
	}
}
