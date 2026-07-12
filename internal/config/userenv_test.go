package config

import (
	"os"
	"path/filepath"
	"testing"
)

// isolateUserConfig points os.UserConfigDir at a temp dir (Linux honors
// XDG_CONFIG_HOME) so tests never touch the developer's real config file.
func isolateUserConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

func TestSetAndLoadUserEnv(t *testing.T) {
	isolateUserConfig(t)
	if err := SetUserEnv("GEMINI_API_KEY", "abc123"); err != nil {
		t.Fatal(err)
	}
	if err := SetUserEnv("SEMIDX_DB_DSN", "postgres://u:p@h:5432/db"); err != nil {
		t.Fatal(err)
	}
	m, err := LoadUserEnv()
	if err != nil {
		t.Fatal(err)
	}
	if m["GEMINI_API_KEY"] != "abc123" || m["SEMIDX_DB_DSN"] != "postgres://u:p@h:5432/db" {
		t.Errorf("round-trip failed: %v", m)
	}

	// The file is written with 0600 (it may hold secrets).
	p, _ := UserEnvPath()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 600", perm)
	}
}

func TestSetUserEnvPreservesOtherKeys(t *testing.T) {
	isolateUserConfig(t)
	if err := SetUserEnv("GROQ_API_KEY", "one"); err != nil {
		t.Fatal(err)
	}
	if err := SetUserEnv("OPENROUTER_API_KEY", "two"); err != nil {
		t.Fatal(err)
	}
	// Overwriting one key must not drop the other.
	if err := SetUserEnv("GROQ_API_KEY", "updated"); err != nil {
		t.Fatal(err)
	}
	m, _ := LoadUserEnv()
	if m["GROQ_API_KEY"] != "updated" || m["OPENROUTER_API_KEY"] != "two" {
		t.Errorf("preserve/update failed: %v", m)
	}
}

func TestUnsetUserEnv(t *testing.T) {
	isolateUserConfig(t)
	_ = SetUserEnv("EMBED_API_KEY", "x")
	if err := UnsetUserEnv("EMBED_API_KEY"); err != nil {
		t.Fatal(err)
	}
	m, _ := LoadUserEnv()
	if _, ok := m["EMBED_API_KEY"]; ok {
		t.Error("key still present after unset")
	}
	// Unsetting an absent key is a no-op, not an error.
	if err := UnsetUserEnv("NEVER_SET"); err != nil {
		t.Errorf("unset absent key errored: %v", err)
	}
}

func TestSetUserEnvRejectsBadInput(t *testing.T) {
	isolateUserConfig(t)
	if err := SetUserEnv("", "v"); err == nil {
		t.Error("empty key should error")
	}
	if err := SetUserEnv("HAS=EQUALS", "v"); err == nil {
		t.Error("key with '=' should error")
	}
	if err := SetUserEnv("K", "line1\nline2"); err == nil {
		t.Error("value with newline should error")
	}
}

func TestUserEnvIsLowestFilePrecedence(t *testing.T) {
	clearEnv(t) // sets XDG_CONFIG_HOME to a temp dir
	// Persist a value in the user config file.
	if err := SetUserEnv("SEMIDX_DB_DSN", "postgres://from-user-file"); err != nil {
		t.Fatal(err)
	}
	// With no project .env and no real env, the user file wins over the default.
	t.Chdir(t.TempDir())
	if got := Load().DatabaseURL; got != "postgres://from-user-file" {
		t.Errorf("DatabaseURL = %q, want the user-file value", got)
	}

	// A project .env overrides the user file.
	writeDotEnv(t, "SEMIDX_DB_DSN=postgres://from-project-env\n")
	if got := Load().DatabaseURL; got != "postgres://from-project-env" {
		t.Errorf("project .env should beat the user file, got %q", got)
	}

	// A real env var beats both.
	t.Setenv("SEMIDX_DB_DSN", "postgres://from-real-env")
	if got := Load().DatabaseURL; got != "postgres://from-real-env" {
		t.Errorf("real env should win, got %q", got)
	}
}

func TestEffectiveValue(t *testing.T) {
	clearEnv(t)
	t.Chdir(t.TempDir())
	if got := EffectiveValue("GEMINI_API_KEY"); got != "" {
		t.Errorf("unset key = %q, want empty", got)
	}
	_ = SetUserEnv("GEMINI_API_KEY", "eff")
	if got := EffectiveValue("GEMINI_API_KEY"); got != "eff" {
		t.Errorf("EffectiveValue = %q, want eff", got)
	}
}

func TestIsSecret(t *testing.T) {
	secret := []string{"GEMINI_API_KEY", "SEMIDX_DB_DSN", "SEMIDX_JWT_SECRET", "SOME_TOKEN", "DB_PASSWORD"}
	for _, k := range secret {
		if !IsSecret(k) {
			t.Errorf("IsSecret(%q) = false, want true", k)
		}
	}
	plain := []string{"SEMIDX_OLLAMA_URL", "SEMIDX_LISTEN_ADDR", "EMBED_PROVIDER"}
	for _, k := range plain {
		if IsSecret(k) {
			t.Errorf("IsSecret(%q) = true, want false", k)
		}
	}
}

func TestKnownKeysAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range KnownKeys {
		if seen[k.Name] {
			t.Errorf("duplicate known key %q", k.Name)
		}
		seen[k.Name] = true
	}
}

func TestUserEnvPathHonorsXDG(t *testing.T) {
	dir := isolateUserConfig(t)
	p, err := UserEnvPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "semidx", "semidx.env"); p != want {
		t.Errorf("UserEnvPath = %q, want %q", p, want)
	}
}
