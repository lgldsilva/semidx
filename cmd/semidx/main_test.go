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
