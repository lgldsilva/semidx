package main

import "testing"

func TestEffectiveConfigProfile(t *testing.T) {
	t.Setenv("SEMIDX_PROFILE", "envp")
	if got := effectiveConfigProfile("flagp"); got != "flagp" {
		t.Fatalf("flag profile should win: got %q", got)
	}
	if got := effectiveConfigProfile(""); got != "envp" {
		t.Fatalf("env profile should be used when flag is empty: got %q", got)
	}
}

func TestResolveBackendModeFromEnv(t *testing.T) {
	t.Setenv("SEMIDX_BACKEND", "remote")
	got, err := resolveBackendMode("")
	if err != nil {
		t.Fatalf("resolveBackendMode from env failed: %v", err)
	}
	if got != backendRemote {
		t.Fatalf("backend from env = %q, want %q", got, backendRemote)
	}
}
