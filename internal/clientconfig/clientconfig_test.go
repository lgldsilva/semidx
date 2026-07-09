package clientconfig

import (
	"path/filepath"
	"testing"
)

// isolate points XDG_CONFIG_HOME at a temp dir and clears the env overrides so
// each test sees a clean slate.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("SEMIDX_SERVER_URL", "")
	t.Setenv("SEMIDX_TOKEN", "")
	t.Setenv("SEMIDX_DEFAULT_PROJECT", "")
	return dir
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	isolate(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.ServerURL != "" || c.Token != "" {
		t.Errorf("expected empty config, got %+v", c)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	dir := isolate(t)
	want := &Config{ServerURL: "https://s.example", Token: "semidx_abc", DefaultProject: "app"}
	if err := Save(want); err != nil {
		t.Fatal(err)
	}
	if p, _ := Path(); p != filepath.Join(dir, "semidx", "config.yaml") {
		t.Errorf("path = %s", p)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if *got != *want {
		t.Errorf("round-trip: got %+v, want %+v", got, want)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	isolate(t)
	if err := Save(&Config{ServerURL: "https://file", Token: "file-tok"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SEMIDX_SERVER_URL", "https://env")
	t.Setenv("SEMIDX_TOKEN", "env-tok")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.ServerURL != "https://env" || c.Token != "env-tok" {
		t.Errorf("env did not override file: %+v", c)
	}
}

func TestRemoveDeletesFile(t *testing.T) {
	isolate(t)
	if err := Save(&Config{ServerURL: "https://s", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := Remove(); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.ServerURL != "" || c.Token != "" {
		t.Errorf("after Remove, Load should be empty, got %+v", c)
	}
	// Idempotent.
	if err := Remove(); err != nil {
		t.Fatal(err)
	}
}
