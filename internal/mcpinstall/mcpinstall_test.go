package mcpinstall

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func init() {
	// Date.now-style nondeterminism is banned in workflows but fine in tests;
	// pin the backup suffix so assertions are stable.
	timestamp = func() string { return "TEST" }
}

// decodeJSON reads a written config file as a generic map.
func decodeJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %s: %v\n%s", path, err, b)
	}
	return m
}

func TestSnippetPerClientShape(t *testing.T) {
	cases := map[string]struct {
		wantKey     string
		wantInEntry string // substring that must appear in the rendered snippet
	}{
		"claude-code":    {"mcpServers", `"command"`},
		"claude-desktop": {"mcpServers", `"command"`},
		"cursor":         {"mcpServers", `"command"`},
		"windsurf":       {"mcpServers", `"command"`},
		"gemini-cli":     {"mcpServers", `"command"`},
		"antigravity":    {"mcpServers", `"command"`},
		"copilot":        {"mcpServers", `"command"`},
		"vscode":         {"servers", `"command"`},
		"opencode":       {"mcp", `"type": "local"`},
		"crush":          {"mcp", `"type": "stdio"`},
	}
	for id, want := range cases {
		_, snip, err := Snippet(Options{Client: id, Name: "semidx", ExePath: "/opt/semidx"})
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if !strings.Contains(snip, want.wantKey) {
			t.Errorf("%s: snippet missing key %q:\n%s", id, want.wantKey, snip)
		}
		if !strings.Contains(snip, want.wantInEntry) {
			t.Errorf("%s: snippet missing %q:\n%s", id, want.wantInEntry, snip)
		}
		if !strings.Contains(snip, "/opt/semidx") {
			t.Errorf("%s: snippet missing exe path:\n%s", id, snip)
		}
	}
}

func TestSnippetCodexIsTOML(t *testing.T) {
	_, snip, err := Snippet(Options{Client: "codex", Name: "semidx", ExePath: "/opt/semidx"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snip, "[mcp_servers.semidx]") || !strings.Contains(snip, `command = "/opt/semidx"`) {
		t.Errorf("codex snippet not TOML:\n%s", snip)
	}
}

func TestSnippetCagentIsYAML(t *testing.T) {
	_, snip, err := Snippet(Options{Client: "cagent", Name: "semidx", ExePath: "/opt/semidx"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snip, "toolsets:") || !strings.Contains(snip, "type: mcp") || !strings.Contains(snip, "/opt/semidx") {
		t.Errorf("cagent snippet not the expected YAML toolset:\n%s", snip)
	}
}

func TestApplyRejectsPrintOnlyClients(t *testing.T) {
	for _, id := range []string{"cagent"} {
		dir := t.TempDir()
		p := filepath.Join(dir, "cfg")
		if _, err := Apply(Options{Client: id, Name: "semidx", ExePath: "/opt/semidx", FilePath: p}); err == nil {
			t.Errorf("%s apply should error (print-only)", id)
		}
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s apply must not write a file", id)
		}
	}
}

func TestApplyCrushWritesStdioEntry(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "crush.json")
	if _, err := Apply(Options{Client: "crush", Name: "semidx", ExePath: "/opt/semidx", FilePath: cfg}); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, cfg)
	entry, ok := m["mcp"].(map[string]any)["semidx"].(map[string]any)
	if !ok {
		t.Fatalf("crush mcp.semidx entry missing: %v", m)
	}
	if entry["type"] != "stdio" || entry["command"] != "/opt/semidx" {
		t.Errorf("crush entry = %v; want type=stdio command=/opt/semidx", entry)
	}
}

func TestSnippetUnknownClient(t *testing.T) {
	if _, _, err := Snippet(Options{Client: "nope"}); err == nil {
		t.Error("unknown client should error")
	}
}

func TestApplyCreatesAndMergesJSON(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "mcp.json")
	// Pre-existing config with an unrelated server we must preserve.
	seed := `{"mcpServers":{"other":{"command":"other-bin"}},"unrelatedTopKey":42}`
	if err := os.WriteFile(cfg, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	written, err := Apply(Options{Client: "cursor", Name: "semidx", ExePath: "/opt/semidx", FilePath: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if written != cfg {
		t.Fatalf("wrote %s; want %s", written, cfg)
	}

	m := decodeJSON(t, cfg)
	// Unrelated top-level key preserved.
	if m["unrelatedTopKey"].(float64) != 42 {
		t.Errorf("unrelated top key lost: %v", m["unrelatedTopKey"])
	}
	servers := m["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Error("pre-existing 'other' server was dropped")
	}
	semidx, ok := servers["semidx"].(map[string]any)
	if !ok {
		t.Fatalf("semidx entry missing: %v", servers)
	}
	if semidx["command"] != "/opt/semidx" {
		t.Errorf("command = %v; want /opt/semidx", semidx["command"])
	}

	// A backup of the original was written.
	if _, err := os.Stat(cfg + ".bak-TEST"); err != nil {
		t.Errorf("no backup written: %v", err)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "settings.json")
	opts := Options{Client: "gemini-cli", Name: "semidx", ExePath: "/usr/local/bin/semidx", FilePath: cfg}

	if _, err := Apply(opts); err != nil {
		t.Fatal(err)
	}
	first := decodeJSON(t, cfg)
	// Change the exe and re-apply: the single entry is replaced, not duplicated.
	opts.ExePath = "/opt/semidx"
	if _, err := Apply(opts); err != nil {
		t.Fatal(err)
	}
	second := decodeJSON(t, cfg)

	servers := second["mcpServers"].(map[string]any)
	if len(servers) != 1 {
		t.Errorf("expected exactly one server after re-apply, got %d: %v", len(servers), servers)
	}
	if servers["semidx"].(map[string]any)["command"] != "/opt/semidx" {
		t.Errorf("re-apply did not update command: %v", servers["semidx"])
	}
	// The shapes match aside from the updated command (structure stable).
	if len(first["mcpServers"].(map[string]any)) != 1 {
		t.Error("first apply should also yield one server")
	}
}

func TestApplyCreatesMissingParentDir(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "nested", "deeper", "mcp.json")
	if _, err := Apply(Options{Client: "vscode", Name: "semidx", ExePath: "/opt/semidx", FilePath: cfg}); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, cfg)
	if _, ok := m["servers"].(map[string]any)["semidx"]; !ok {
		t.Errorf("vscode 'servers' entry missing: %v", m)
	}
}

func TestApplyCodexTOML(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	// Pre-existing TOML with another mcp_server we must preserve.
	seed := "[mcp_servers.ai-memory]\nurl = \"http://localhost/mcp\"\n"
	if err := os.WriteFile(cfg, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	written, err := Apply(Options{Client: "codex", Name: "semidx", ExePath: "/opt/semidx", FilePath: cfg})
	if err != nil {
		t.Fatalf("codex apply should succeed: %v", err)
	}
	if written != cfg {
		t.Fatalf("wrote %s; want %s", written, cfg)
	}
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	// Verify semidx section was added.
	if !strings.Contains(content, "[mcp_servers.semidx]") {
		t.Errorf("missing semidx section:\n%s", content)
	}
	if !strings.Contains(content, `"/opt/semidx"`) {
		t.Errorf("missing exe path:\n%s", content)
	}
	// Existing ai-memory section preserved.
	if !strings.Contains(content, "[mcp_servers.ai-memory]") {
		t.Errorf("pre-existing ai-memory section lost:\n%s", content)
	}
	// Backup was written.
	if _, err := os.Stat(cfg + ".bak-TEST"); err != nil {
		t.Errorf("no backup written: %v", err)
	}
	// Idempotent: re-apply does not duplicate.
	if _, err := Apply(Options{Client: "codex", Name: "semidx", ExePath: "/opt/semidx", FilePath: cfg}); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(cfg)
	if strings.Count(string(b2), "[mcp_servers.semidx]") != 1 {
		t.Errorf("re-apply duplicated the semidx section:\n%s", string(b2))
	}
}

func TestApplyRejectsInvalidExistingJSON(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(cfg, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(Options{Client: "cursor", Name: "semidx", ExePath: "/opt/semidx", FilePath: cfg}); err == nil {
		t.Error("apply over malformed JSON should error, not clobber")
	}
}

func TestClientIDsAndListCoverAll(t *testing.T) {
	ids := ClientIDs()
	if len(ids) != len(Clients) {
		t.Errorf("ClientIDs returned %d; want %d", len(ids), len(Clients))
	}
	list := ClientList()
	for _, c := range Clients {
		if !strings.Contains(list, c.ID) {
			t.Errorf("ClientList missing %q", c.ID)
		}
	}
}

func TestDefaultPathsAreClientAppropriate(t *testing.T) {
	home := "/home/u"
	config := "/home/u/.config"
	project := "/work/proj"
	cases := map[string]string{
		"claude-code":    filepath.Join(project, ".mcp.json"),
		"claude-desktop": filepath.Join(config, "Claude", "claude_desktop_config.json"),
		"cursor":         filepath.Join(home, ".cursor", "mcp.json"),
		"windsurf":       filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"),
		"gemini-cli":     filepath.Join(home, ".gemini", "settings.json"),
		"antigravity":    filepath.Join(home, ".gemini", "antigravity-cli", "mcp_config.json"),
		"vscode":         filepath.Join(project, ".vscode", "mcp.json"),
		"copilot":        filepath.Join(config, "github-copilot", "mcp-config.json"),
		"opencode":       filepath.Join(project, "opencode.json"),
		"crush":          filepath.Join(config, "crush", "crush.json"),
		"codex":          filepath.Join(home, ".codex", "config.toml"),
	}
	for id, want := range cases {
		path, _, err := Snippet(Options{Client: id, Name: "semidx", ExePath: "x", Home: home, ConfigDir: config, Project: project})
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if path != want {
			t.Errorf("%s default path = %s; want %s", id, path, want)
		}
	}
}

func TestApplyableClientsAndIsApplyable(t *testing.T) {
	applyable := ApplyableClients()
	if len(applyable) == 0 {
		t.Fatal("expected at least one applyable client")
	}
	for _, c := range applyable {
		if !c.IsApplyable() {
			t.Errorf("ApplyableClients returned non-applyable client %q", c.ID)
		}
	}
	// codex and cagent are print-only, so applyable must be a strict subset.
	if len(applyable) >= len(Clients) {
		t.Errorf("expected some print-only clients; applyable=%d total=%d", len(applyable), len(Clients))
	}
}
