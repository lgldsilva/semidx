package main

import (
	"testing"
)

func TestSystemDirsBlocked(t *testing.T) {
	if !systemDirs["/"] {
		t.Error("/ should be blocked")
	}
	if !systemDirs["/etc"] {
		t.Error("/etc should be blocked")
	}
	if systemDirs["/home/user/project"] {
		t.Error("/home/user/project should NOT be blocked")
	}
}

func TestDocsFlagHint(t *testing.T) {
	if docsFlagHint(true) != " --docs" {
		t.Errorf("docsFlagHint(true) = %q, want ' --docs'", docsFlagHint(true))
	}
	if docsFlagHint(false) != "" {
		t.Errorf("docsFlagHint(false) = %q, want ''", docsFlagHint(false))
	}
}

func TestApplyBranchSuffix(t *testing.T) {
	t.Run("empty branch is no-op", func(t *testing.T) {
		tgt := indexTarget{
			identity:   "remote:github.com/org/repo",
			name:       "repo",
			sourceType: "git",
		}
		got := applyBranchSuffix(tgt, "")
		if got.identity != "remote:github.com/org/repo" {
			t.Errorf("identity = %q, want unchanged", got.identity)
		}
		if got.name != "repo" {
			t.Errorf("name = %q, want unchanged", got.name)
		}
	})

	t.Run("appends branch to git identity and name", func(t *testing.T) {
		tgt := indexTarget{
			identity:   "remote:github.com/org/repo",
			name:       "repo",
			sourceType: "git",
		}
		got := applyBranchSuffix(tgt, "develop")
		if got.identity != "remote:github.com/org/repo#develop" {
			t.Errorf("identity = %q, want %q", got.identity, "remote:github.com/org/repo#develop")
		}
		if got.name != "repo@develop" {
			t.Errorf("name = %q, want %q", got.name, "repo@develop")
		}
	})

	t.Run("ignored for docs projects", func(t *testing.T) {
		tgt := indexTarget{
			identity:   "path:/some/docs",
			name:       "docs",
			sourceType: "docs",
		}
		got := applyBranchSuffix(tgt, "develop")
		if got.identity != "path:/some/docs" {
			t.Errorf("identity = %q, want unchanged", got.identity)
		}
		if got.name != "docs" {
			t.Errorf("name = %q, want unchanged", got.name)
		}
	})

	t.Run("local git repo", func(t *testing.T) {
		tgt := indexTarget{
			identity:   "local:/home/user/project/.git",
			name:       "project",
			sourceType: "git",
		}
		got := applyBranchSuffix(tgt, "feature/x")
		if got.identity != "local:/home/user/project/.git#feature/x" {
			t.Errorf("identity = %q, want %q", got.identity, "local:/home/user/project/.git#feature/x")
		}
		if got.name != "project@feature/x" {
			t.Errorf("name = %q, want %q", got.name, "project@feature/x")
		}
	})
}
