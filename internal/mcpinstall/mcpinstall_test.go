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
		"pi":             {"mcpServers", `"command"`},
		"kimi":           {"mcpServers", `"command"`},
		"mimo":           {"mcp", `"type": "local"`},
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

func TestClientAliasAgyAndClaude(t *testing.T) {
	if CanonicalClientID("agy") != "antigravity" {
		t.Error(`CanonicalClientID("agy") should resolve to antigravity`)
	}
	if CanonicalClientID("vscode") != "vscode" {
		t.Error("a canonical id must pass through unchanged")
	}
	c, ok := clientByID("agy")
	if !ok || c.ID != "antigravity" {
		t.Fatalf("agy resolve = %+v ok=%v", c, ok)
	}
	c, ok = clientByID("claude")
	if !ok || c.ID != "claude-code" {
		t.Fatalf("claude resolve = %+v ok=%v", c, ok)
	}
	path, _, err := Snippet(Options{Client: "agy", Name: "semidx", ExePath: "/x", Home: "/h", ConfigDir: "/c", Project: "/p"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(path, "antigravity-cli") {
		t.Errorf("agy path = %s", path)
	}
}

func TestKimiHomeEnvOverride(t *testing.T) {
	t.Setenv("KIMI_CODE_HOME", "/custom/kimi")
	path, _, err := Snippet(Options{Client: "kimi", Name: "semidx", ExePath: "/x", Home: "/h"})
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join("/custom/kimi", "mcp.json") {
		t.Fatalf("path=%s", path)
	}
}

// TestMimoFallsBackToHomeConfig covers the empty-ConfigDir path, which only
// mimo has: os.UserConfigDir can fail, and the entry must still resolve.
func TestMimoFallsBackToHomeConfig(t *testing.T) {
	path, _, err := Snippet(Options{Client: "mimo", Name: "semidx", ExePath: "/x", Home: "/h"})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/h", ".config", "mimocode", "mimocode.json"); path != want {
		t.Errorf("mimo path = %s; want %s", path, want)
	}
}

func TestClientListMentionsAliases(t *testing.T) {
	list := ClientList()
	for _, want := range []string{"agy→antigravity", "claude→claude-code"} {
		if !strings.Contains(list, want) {
			t.Errorf("ClientList missing alias %q:\n%s", want, list)
		}
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
		"pi":             filepath.Join(home, ".pi", "agent", "mcp.json"),
		"kimi":           filepath.Join(home, ".kimi-code", "mcp.json"),
		"mimo":           filepath.Join(config, "mimocode", "mimocode.json"),
	}
	// kimi honours $KIMI_CODE_HOME, so a developer with it set would otherwise
	// see a spurious failure here.
	t.Setenv("KIMI_CODE_HOME", "")
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
	// cagent is print-only, so applyable must be a strict subset.
	if len(applyable) >= len(Clients) {
		t.Errorf("expected some print-only clients; applyable=%d total=%d", len(applyable), len(Clients))
	}
}

func TestSnippetEnvPerFormat(t *testing.T) {
	env := map[string]string{"SEMIDX_LOCAL_INDEX": "1", "SEMIDX_OLLAMA_URL": "http://gpu:11434"}
	cases := map[string][]string{
		"claude-code": {`"env"`, `"SEMIDX_LOCAL_INDEX": "1"`},
		"vscode":      {`"env"`, `"SEMIDX_OLLAMA_URL": "http://gpu:11434"`},
		"crush":       {`"env"`, `"SEMIDX_LOCAL_INDEX": "1"`},
		"opencode":    {`"environment"`, `"SEMIDX_LOCAL_INDEX": "1"`},
		"codex":       {`env = { SEMIDX_LOCAL_INDEX = "1", SEMIDX_OLLAMA_URL = "http://gpu:11434" }`},
		"cagent":      {"    env:\n", `      SEMIDX_LOCAL_INDEX: "1"`},
	}
	for id, wants := range cases {
		_, snip, err := Snippet(Options{Client: id, Name: "semidx", ExePath: "/opt/semidx", Env: env})
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		for _, w := range wants {
			if !strings.Contains(snip, w) {
				t.Errorf("%s: snippet missing %q:\n%s", id, w, snip)
			}
		}
	}
	// opencode must NOT use "env" (its key is "environment").
	_, snip, err := Snippet(Options{Client: "opencode", Name: "semidx", ExePath: "/opt/semidx", Env: env})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(snip, `"env"`) {
		t.Errorf("opencode snippet must use \"environment\", not \"env\":\n%s", snip)
	}
}

func TestSnippetWithoutEnvHasNoEnvBlock(t *testing.T) {
	for _, marker := range []string{`"env":`, `"environment":`, "env = {", "    env:"} {
		for _, id := range []string{"claude-code", "vscode", "opencode", "crush", "codex", "cagent"} {
			_, snip, err := Snippet(Options{Client: id, Name: "semidx", ExePath: "/opt/semidx"})
			if err != nil {
				t.Fatalf("%s: %v", id, err)
			}
			if strings.Contains(snip, marker) {
				t.Errorf("%s: snippet without Env must not contain %q:\n%s", id, marker, snip)
			}
		}
	}
}

func TestEnvKeyOrderingIsStable(t *testing.T) {
	env := map[string]string{"ZZZ": "3", "AAA": "1", "MMM": "2"}
	// TOML inline table: sorted keys.
	_, toml, err := Snippet(Options{Client: "codex", Name: "semidx", ExePath: "/x", Env: env})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(toml, `env = { AAA = "1", MMM = "2", ZZZ = "3" }`) {
		t.Errorf("codex env keys not sorted:\n%s", toml)
	}
	// YAML: sorted keys, one per line.
	_, yaml, err := Snippet(Options{Client: "cagent", Name: "semidx", ExePath: "/x", Env: env})
	if err != nil {
		t.Fatal(err)
	}
	a, m, z := strings.Index(yaml, "AAA:"), strings.Index(yaml, "MMM:"), strings.Index(yaml, "ZZZ:")
	if a < 0 || m < 0 || z < 0 || a >= m || m >= z {
		t.Errorf("cagent env keys not sorted (AAA@%d MMM@%d ZZZ@%d):\n%s", a, m, z, yaml)
	}
	// JSON: encoding/json sorts map keys on marshal.
	_, js, err := Snippet(Options{Client: "cursor", Name: "semidx", ExePath: "/x", Env: env})
	if err != nil {
		t.Fatal(err)
	}
	a, m, z = strings.Index(js, `"AAA"`), strings.Index(js, `"MMM"`), strings.Index(js, `"ZZZ"`)
	if a < 0 || m < 0 || z < 0 || a >= m || m >= z {
		t.Errorf("cursor env keys not sorted (AAA@%d MMM@%d ZZZ@%d):\n%s", a, m, z, js)
	}
	// EnvKeys itself.
	got := EnvKeys(env)
	if len(got) != 3 || got[0] != "AAA" || got[1] != "MMM" || got[2] != "ZZZ" {
		t.Errorf("EnvKeys = %v; want [AAA MMM ZZZ]", got)
	}
}

func TestApplyEnvPreservesOtherServersEnv(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "mcp.json")
	seed := `{"mcpServers":{"other":{"command":"other-bin","env":{"OTHER_TOKEN":"keep-me"}}}}`
	if err := os.WriteFile(cfg, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := Options{Client: "cursor", Name: "semidx", ExePath: "/opt/semidx", FilePath: cfg,
		Env: map[string]string{"SEMIDX_LOCAL_INDEX": "1"}}
	if _, err := Apply(opts); err != nil {
		t.Fatal(err)
	}
	// Re-apply: idempotent, no duplication, other server's env intact.
	if _, err := Apply(opts); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, cfg)
	servers := m["mcpServers"].(map[string]any)
	if len(servers) != 2 {
		t.Fatalf("expected 2 servers after re-apply, got %d: %v", len(servers), servers)
	}
	otherEnv, ok := servers["other"].(map[string]any)["env"].(map[string]any)
	if !ok || otherEnv["OTHER_TOKEN"] != "keep-me" {
		t.Errorf("other server's env was not preserved: %v", servers["other"])
	}
	semidxEnv, ok := servers["semidx"].(map[string]any)["env"].(map[string]any)
	if !ok || semidxEnv["SEMIDX_LOCAL_INDEX"] != "1" {
		t.Errorf("semidx env missing/wrong: %v", servers["semidx"])
	}
}

func TestApplyEnvReplacesStaleEnv(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "mcp.json")
	opts := Options{Client: "cursor", Name: "semidx", ExePath: "/opt/semidx", FilePath: cfg,
		Env: map[string]string{"OLD_VAR": "old"}}
	if _, err := Apply(opts); err != nil {
		t.Fatal(err)
	}
	opts.Env = map[string]string{"NEW_VAR": "new"}
	if _, err := Apply(opts); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, cfg)
	env := m["mcpServers"].(map[string]any)["semidx"].(map[string]any)["env"].(map[string]any)
	if _, stale := env["OLD_VAR"]; stale {
		t.Errorf("stale OLD_VAR survived re-apply: %v", env)
	}
	if env["NEW_VAR"] != "new" {
		t.Errorf("NEW_VAR missing: %v", env)
	}
}

func TestApplyCodexTOMLWithEnv(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	// Pre-existing semidx section (stale env) in the MIDDLE, followed by another
	// section that must survive untouched.
	seed := "[mcp_servers.semidx]\ncommand = \"/old/semidx\"\nargs = [\"mcp\"]\nenv = { STALE = \"x\" }\n\n" +
		"[mcp_servers.ai-memory]\nurl = \"http://localhost/mcp\"\n"
	if err := os.WriteFile(cfg, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := Options{Client: "codex", Name: "semidx", ExePath: "/opt/semidx", FilePath: cfg,
		Env: map[string]string{"SEMIDX_OLLAMA_URL": "http://gpu:11434", "SEMIDX_LOCAL_INDEX": "1"}}
	if _, err := Apply(opts); err != nil {
		t.Fatal(err)
	}
	// Idempotency: apply twice.
	if _, err := Apply(opts); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	if strings.Count(content, "[mcp_servers.semidx]") != 1 {
		t.Errorf("semidx section duplicated:\n%s", content)
	}
	if !strings.Contains(content, `env = { SEMIDX_LOCAL_INDEX = "1", SEMIDX_OLLAMA_URL = "http://gpu:11434" }`) {
		t.Errorf("sorted env inline table missing:\n%s", content)
	}
	if strings.Contains(content, "STALE") {
		t.Errorf("stale env survived the section replace:\n%s", content)
	}
	if !strings.Contains(content, "[mcp_servers.ai-memory]") || !strings.Contains(content, `url = "http://localhost/mcp"`) {
		t.Errorf("following ai-memory section lost:\n%s", content)
	}
	if !strings.Contains(content, `command = "/opt/semidx"`) || strings.Contains(content, "/old/semidx") {
		t.Errorf("command not replaced:\n%s", content)
	}
}

func TestTomlKeyQuotesNonBareKeys(t *testing.T) {
	env := map[string]string{"MY.DOTTED KEY": "v"}
	_, snip, err := Snippet(Options{Client: "codex", Name: "semidx", ExePath: "/x", Env: env})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snip, `"MY.DOTTED KEY" = "v"`) {
		t.Errorf("non-bare TOML key not quoted:\n%s", snip)
	}
}

func TestParseEnv(t *testing.T) {
	t.Run("empty input is nil", func(t *testing.T) {
		if env, err := ParseEnv(nil); err != nil || env != nil {
			t.Errorf("ParseEnv(nil) = %v, %v; want nil, nil", env, err)
		}
	})
	t.Run("valid pairs, value may contain =", func(t *testing.T) {
		env, err := ParseEnv([]string{"A=1", "DSN=postgres://u:p@h/db?sslmode=disable", "EMPTY="})
		if err != nil {
			t.Fatal(err)
		}
		if env["A"] != "1" || env["DSN"] != "postgres://u:p@h/db?sslmode=disable" || env["EMPTY"] != "" {
			t.Errorf("ParseEnv = %v", env)
		}
	})
	t.Run("missing = errors", func(t *testing.T) {
		if _, err := ParseEnv([]string{"NOVALUE"}); err == nil {
			t.Error("want error for pair without '='")
		}
	})
	t.Run("empty key errors", func(t *testing.T) {
		if _, err := ParseEnv([]string{"=v"}); err == nil {
			t.Error("want error for empty key")
		}
	})
}

func TestLooksLikeSecret(t *testing.T) {
	secret := []string{"GEMINI_API_KEY", "MY_TOKEN", "clientSecret", "DB_PASSWORD", "ApiKey", "token"}
	for _, k := range secret {
		if !LooksLikeSecret(k) {
			t.Errorf("LooksLikeSecret(%q) = false; want true", k)
		}
	}
	plain := []string{"SEMIDX_LOCAL_INDEX", "PATH", "OLLAMA_HOST", "DEBUG"}
	for _, k := range plain {
		if LooksLikeSecret(k) {
			t.Errorf("LooksLikeSecret(%q) = true; want false", k)
		}
	}
}

func TestApplyUnknownClient(t *testing.T) {
	if _, err := Apply(Options{Client: "nope"}); err == nil {
		t.Error("Apply with unknown client should error")
	}
}

func TestApplyRejectsRelativePathOutsideProject(t *testing.T) {
	opts := Options{Client: "cursor", Name: "semidx", ExePath: "/x",
		FilePath: filepath.Join("relative", "mcp.json"), Project: "/proj"}
	if _, err := Apply(opts); err == nil {
		t.Error("relative config path outside the project should error")
	}
}

func TestApplyMkdirFailsWhenParentIsFile(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := Options{Client: "cursor", Name: "semidx", ExePath: "/x",
		FilePath: filepath.Join(blocker, "sub", "mcp.json")}
	if _, err := Apply(opts); err == nil {
		t.Error("MkdirAll under a regular file should error")
	}
}

func TestApplyCodexTOMLCreatesFileWithEnv(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	opts := Options{Client: "codex", Name: "semidx", ExePath: "/opt/semidx", FilePath: cfg,
		Env: map[string]string{"A": "1"}}
	if _, err := Apply(opts); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	if !strings.HasPrefix(content, "[mcp_servers.semidx]\n") || !strings.Contains(content, `env = { A = "1" }`) {
		t.Errorf("fresh codex config wrong:\n%s", content)
	}
}

func TestMergeTomlCodexHeaderWithTrailingComment(t *testing.T) {
	// Header followed by a space (inline comment) still matches for replacement.
	seed := "[mcp_servers.semidx] # managed\ncommand = \"/old\"\n\n[other]\nk = 1\n"
	out := string(mergeTomlCodex([]byte(seed), "semidx", "/new", map[string]string{"B": "2"}))
	if strings.Contains(out, "/old") || !strings.Contains(out, `command = "/new"`) {
		t.Errorf("section with trailing comment not replaced:\n%s", out)
	}
	if !strings.Contains(out, "[other]") || !strings.Contains(out, "k = 1") {
		t.Errorf("following section lost:\n%s", out)
	}
	if !strings.Contains(out, `env = { B = "2" }`) {
		t.Errorf("env line missing:\n%s", out)
	}
}

func TestMergeTomlCodexAppendsToFileWithoutTrailingNewline(t *testing.T) {
	seed := "[other]\nk = 1" // no trailing newline
	out := string(mergeTomlCodex([]byte(seed), "semidx", "/x", nil))
	if !strings.Contains(out, "k = 1\n") || !strings.Contains(out, "[mcp_servers.semidx]") {
		t.Errorf("append to newline-less file broken:\n%s", out)
	}
}

func TestWriteConfigFileRenameFails(t *testing.T) {
	dir := t.TempDir()
	if err := writeConfigFile(filepath.Join(dir, "missing", "f.json"), []byte("x"), 0o600); err == nil {
		t.Error("rename into a missing directory should error")
	}
}
