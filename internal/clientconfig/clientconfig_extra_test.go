package clientconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultProjectEnvOverride(t *testing.T) {
	isolate(t)
	if err := Save(&Config{ServerURL: "https://s", Token: "t", DefaultProject: "file-proj"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SEMIDX_DEFAULT_PROJECT", "env-proj")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.DefaultProject != "env-proj" {
		t.Errorf("DefaultProject = %q, want env override 'env-proj'", c.DefaultProject)
	}
}

func TestLoadTenantAndWorkspaceEnvOverride(t *testing.T) {
	isolate(t)
	if err := Save(&Config{Tenant: "file-tenant", Workspace: "file-workspace"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SEMIDX_TENANT", "env-tenant")
	t.Setenv("SEMIDX_WORKSPACE", "env-workspace")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Tenant != "env-tenant" || c.Workspace != "env-workspace" {
		t.Fatalf("tenant/workspace = %q/%q", c.Tenant, c.Workspace)
	}
}

func TestLoadMalformedYAML(t *testing.T) {
	dir := isolate(t)
	p := filepath.Join(dir, "semidx", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	// A YAML scalar where a mapping is expected fails to unmarshal into Config.
	if err := os.WriteFile(p, []byte("\tthis: : is: not: valid\n:::"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Error("Load should return an error for malformed YAML")
	}
}

func TestLoadReadErrorNotNotExist(t *testing.T) {
	dir := isolate(t)
	// Make the config path a directory: ReadFile then fails with an error that is
	// not os.IsNotExist, exercising that branch.
	p := filepath.Join(dir, "semidx", "config.yaml")
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Error("Load should surface a non-NotExist read error")
	}
}

func TestSaveMkdirError(t *testing.T) {
	dir := isolate(t)
	// Occupy the parent directory path with a file so MkdirAll fails.
	semidxDir := filepath.Join(dir, "semidx")
	if err := os.WriteFile(semidxDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(&Config{ServerURL: "https://s"}); err == nil {
		t.Error("Save should fail when the config dir cannot be created")
	}
}

func TestPathLoadSaveErrorWithoutHome(t *testing.T) {
	// With neither XDG_CONFIG_HOME nor HOME set, os.UserConfigDir errors and every
	// entry point that resolves the path propagates it.
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	if _, err := Path(); err == nil {
		t.Error("Path should error without XDG_CONFIG_HOME or HOME")
	}
	if _, err := Load(); err == nil {
		t.Error("Load should error without a resolvable config dir")
	}
	if err := Save(&Config{}); err == nil {
		t.Error("Save should error without a resolvable config dir")
	}
}
