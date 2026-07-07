package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/clientconfig"
	"github.com/lgldsilva/semidx/internal/config"
)

// runCLI executes the root command with args in an isolated user-config dir so
// `config` writes never touch the developer's real ~/.config.
func runCLI(t *testing.T, args ...string) error {
	t.Helper()
	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(&strings.Builder{})
	root.SetErr(&strings.Builder{})
	return root.ExecuteContext(context.Background())
}

func TestConfigSetGetUnsetRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// A clean project dir so a stray ./.env can't shadow the user file.
	t.Chdir(t.TempDir())

	if err := runCLI(t, "config", "set", "GEMINI_API_KEY", "secret-xyz"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := config.EffectiveValue("GEMINI_API_KEY"); got != "secret-xyz" {
		t.Fatalf("after set, effective = %q", got)
	}

	// Persisted to the user file, not just this process.
	m, _ := config.LoadUserEnv()
	if m["GEMINI_API_KEY"] != "secret-xyz" {
		t.Errorf("not persisted: %v", m)
	}

	if err := runCLI(t, "config", "unset", "GEMINI_API_KEY"); err != nil {
		t.Fatalf("unset: %v", err)
	}
	if got := config.EffectiveValue("GEMINI_API_KEY"); got != "" {
		t.Errorf("after unset, effective = %q, want empty", got)
	}
}

func TestConfigListKeysPathRun(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	// Seed a couple values so list has something to print (secret + non-secret).
	if err := runCLI(t, "config", "set", "GEMINI_API_KEY", "abcd1234"); err != nil {
		t.Fatal(err)
	}
	if err := runCLI(t, "config", "set", "SEMIDX_OLLAMA_URL", "http://h:11434"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"config", "list"},
		{"config", "list", "--show-secrets"},
		{"config", "keys"},
		{"config", "path"},
	} {
		if err := runCLI(t, args...); err != nil {
			t.Errorf("`semidx %s` errored: %v", strings.Join(args, " "), err)
		}
	}
}

func TestConfigSetUnknownKeyStillWorks(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	// An unknown key warns but is still persisted (forward-compatible).
	if err := runCLI(t, "config", "set", "SOME_FUTURE_KEY", "v"); err != nil {
		t.Fatal(err)
	}
	if got := config.EffectiveValue("SOME_FUTURE_KEY"); got != "v" {
		t.Errorf("unknown key not persisted, got %q", got)
	}
}

func TestConfigGetUnsetKeyErrors(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	if err := runCLI(t, "config", "get", "NEVER_SET_KEY"); err == nil {
		t.Error("get on unset key should error")
	}
}

func TestConfigCommandsWiredIntoTree(t *testing.T) {
	root := newRootCmd()
	for _, path := range [][]string{
		{"config", "set"}, {"config", "get"}, {"config", "unset"},
		{"config", "list"}, {"config", "keys"}, {"config", "path"},
		{"mcp", "install"},
	} {
		cmd, _, err := root.Find(path)
		if err != nil || cmd == nil || cmd.Name() != path[len(path)-1] {
			t.Errorf("command `semidx %s` not wired into the tree", strings.Join(path, " "))
		}
	}
}

func TestMCPInstallCommand(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())

	// --list and a dry-run print and exit 0 without touching disk.
	if err := runCLI(t, "mcp", "install", "--list"); err != nil {
		t.Errorf("mcp install --list: %v", err)
	}
	if err := runCLI(t, "mcp", "install", "--client", "cursor"); err != nil {
		t.Errorf("mcp install dry-run: %v", err)
	}

	// --apply writes the client config at the overridden path.
	cfg := filepath.Join(t.TempDir(), "mcp.json")
	if err := runCLI(t, "mcp", "install", "--client", "cursor", "--config-file", cfg, "--apply"); err != nil {
		t.Fatalf("mcp install --apply: %v", err)
	}
	data, err := os.ReadFile(filepath.Clean(cfg))
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if !strings.Contains(string(data), "mcpServers") || !strings.Contains(string(data), "semidx") {
		t.Errorf("written config missing expected keys:\n%s", data)
	}

	// An unknown client is a clean error.
	if err := runCLI(t, "mcp", "install", "--client", "bogus"); err == nil {
		t.Error("unknown client should error")
	}
}

func TestActiveBackendPrecedence(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())

	// Default (nothing set): Postgres with the hint to configure it.
	if got := activeBackend(&deps{}); !strings.Contains(got, "Postgres") {
		t.Errorf("default backend = %q, want Postgres hint", got)
	}

	// Postgres DSN set → Postgres, DSN masked (password not leaked).
	t.Setenv("SEMIDX_DB_DSN", "postgres://u:supersecret@h:5432/db")
	if got := activeBackend(&deps{}); !strings.Contains(got, "Postgres") || strings.Contains(got, "supersecret") {
		t.Errorf("backend = %q; must be Postgres and must not leak the password", got)
	}

	// With Postgres configured, SEMIDX_LOCAL_INDEX is ignored by default (Postgres wins over SQLite).
	t.Setenv("SEMIDX_LOCAL_INDEX", "/tmp/idx.db")
	if got := activeBackend(&deps{}); !strings.Contains(got, "Postgres") {
		t.Errorf("with DSN set, Postgres should win over SQLite env var, got %q", got)
	}

	// SQLite wins only if DSN is not set (cleared/empty).
	t.Setenv("SEMIDX_DB_DSN", "")
	if got := activeBackend(&deps{}); !strings.Contains(got, "SQLite") {
		t.Errorf("SQLite should win over Postgres default, got %q", got)
	}

	// If LocalIndexPath is explicitly forced via config/flag, SQLite wins even if DSN is set.
	t.Setenv("SEMIDX_DB_DSN", "postgres://u:supersecret@h:5432/db")
	dLocal := &deps{cfg: &config.Config{LocalIndexPath: "/tmp/idx.db"}}
	if got := activeBackend(dLocal); !strings.Contains(got, "SQLite") {
		t.Errorf("explicitly forced LocalIndexPath should win over Postgres DSN, got %q", got)
	}

	// Remote server wins over everything.
	d := &deps{client: &clientconfig.Config{ServerURL: "http://server:8080"}}
	if got := activeBackend(d); !strings.Contains(got, "remote server") {
		t.Errorf("remote should win, got %q", got)
	}
}

func TestKeywordBackendLabel(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	t.Setenv("SEMIDX_EMBED_MODE", "none")
	if got := activeBackend(&deps{}); !strings.Contains(got, "keyword-only") {
		t.Errorf("backend = %q, want keyword-only", got)
	}
}

func TestMaskAndDisplayValue(t *testing.T) {
	if got := mask("abcdefgh"); got != "****efgh" {
		t.Errorf("mask long = %q", got)
	}
	if got := mask("ab"); got != "****" {
		t.Errorf("mask short = %q, want ****", got)
	}
	// Non-secret keys are shown verbatim; secrets are masked unless requested.
	if got := displayValue("SEMIDX_OLLAMA_URL", "http://h:11434", false); got != "http://h:11434" {
		t.Errorf("non-secret masked: %q", got)
	}
	if got := displayValue("GEMINI_API_KEY", "abcdefgh", false); got != "****efgh" {
		t.Errorf("secret not masked: %q", got)
	}
	if got := displayValue("GEMINI_API_KEY", "abcdefgh", true); got != "abcdefgh" {
		t.Errorf("--show-secrets should reveal: %q", got)
	}
}
