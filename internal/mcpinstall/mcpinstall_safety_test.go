package mcpinstall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestApplyWritesTempFileNextToConfig guards the cross-filesystem rename bug:
// the temp file used to live in os.TempDir(), so a tmpfs /tmp and a $HOME on
// another filesystem made the final rename fail with EXDEV. Pointing TMPDIR at
// a directory that does not exist reproduces "temp dir unusable" portably.
func TestApplyWritesTempFileNextToConfig(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "does-not-exist"))
	cfg := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(cfg, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(Options{Client: "cursor", Name: "semidx", ExePath: "/opt/semidx", FilePath: cfg}); err != nil {
		t.Fatalf("apply must not depend on os.TempDir(): %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(cfg))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".semidx-mcp-") {
			t.Errorf("leftover temp file %s", e.Name())
		}
	}
}

func TestApplyPreservesExistingFileMode(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(cfg, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfg, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(Options{Client: "cursor", Name: "semidx", ExePath: "/x", FilePath: cfg}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Errorf("mode = %o, want the original 640", got)
	}
}

func TestApplyNewFileIsPrivate(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "mcp.json")
	if _, err := Apply(Options{Client: "cursor", Name: "semidx", ExePath: "/x", FilePath: cfg}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("new config mode = %o, want 600", got)
	}
}

// TestApplyAbortsOnUnreadableConfig: an existing config that cannot be read
// must not be silently replaced by a file holding only the semidx entry.
func TestApplyAbortsOnUnreadableConfig(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	cfg := filepath.Join(t.TempDir(), "mcp.json")
	orig := `{"mcpServers":{"other":{"command":"/bin/other"}}}`
	if err := os.WriteFile(cfg, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfg, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(cfg, 0o600) })
	if _, err := Apply(Options{Client: "cursor", Name: "semidx", ExePath: "/x", FilePath: cfg}); err == nil {
		t.Fatal("apply over an unreadable config must error")
	}
	if err := os.Chmod(cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != orig {
		t.Errorf("unreadable config was clobbered:\n%s", b)
	}
}

// TestApplyOverJSONNull: a config holding the JSON literal null used to leave
// the root map nil and panic on assignment.
func TestApplyOverJSONNull(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(cfg, []byte("null\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(Options{Client: "cursor", Name: "semidx", ExePath: "/x", FilePath: cfg}); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, cfg)
	if _, ok := m["mcpServers"].(map[string]any)["semidx"]; !ok {
		t.Errorf("semidx entry missing: %v", m)
	}
}
