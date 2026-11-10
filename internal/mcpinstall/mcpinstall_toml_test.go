package mcpinstall

import (
	"strings"
	"testing"
)

// TestMergeTomlCodexReplacesSubtables: an existing [mcp_servers.semidx.env]
// subtable must go away with its parent, otherwise the new inline `env = {…}`
// defines the key twice and Codex rejects the whole file.
func TestMergeTomlCodexReplacesSubtables(t *testing.T) {
	seed := "[mcp_servers.semidx]\ncommand = \"/old\"\nargs = [\"mcp\"]\n\n" +
		"[mcp_servers.semidx.env]\nSTALE = \"x\"\n\n" +
		"[mcp_servers.other]\ncommand = \"/bin/other\"\n\n" +
		"[mcp_servers.other.env]\nKEEP = \"y\"\n"
	out := string(mergeTomlCodex([]byte(seed), "semidx", "/new", map[string]string{"A": "1"}))
	if strings.Contains(out, "[mcp_servers.semidx.env]") || strings.Contains(out, "STALE") {
		t.Errorf("stale semidx subtable survived:\n%s", out)
	}
	if strings.Count(out, "[mcp_servers.semidx]") != 1 || !strings.Contains(out, `env = { A = "1" }`) {
		t.Errorf("semidx section not replaced once:\n%s", out)
	}
	for _, want := range []string{"[mcp_servers.other]", "[mcp_servers.other.env]", `KEEP = "y"`} {
		if !strings.Contains(out, want) {
			t.Errorf("unrelated %q lost:\n%s", want, out)
		}
	}
}

// TestMergeTomlCodexOrphanSubtable: TOML allows a subtable without its parent
// header; it must still be replaced, not left beside the appended section.
func TestMergeTomlCodexOrphanSubtable(t *testing.T) {
	seed := "[other]\nk = 1\n\n[mcp_servers.semidx.env]\nSTALE = \"x\"\n"
	out := string(mergeTomlCodex([]byte(seed), "semidx", "/new", map[string]string{"A": "1"}))
	if strings.Contains(out, "STALE") || strings.Contains(out, "[mcp_servers.semidx.env]") {
		t.Errorf("orphan subtable survived:\n%s", out)
	}
	if !strings.Contains(out, "[other]\nk = 1\n") || !strings.Contains(out, "[mcp_servers.semidx]\n") {
		t.Errorf("merge lost content:\n%s", out)
	}
}

func TestMergeTomlCodexKeepsSimilarlyNamedSection(t *testing.T) {
	seed := "[mcp_servers.semidx-dev]\ncommand = \"/dev\"\n"
	out := string(mergeTomlCodex([]byte(seed), "semidx", "/new", nil))
	if !strings.Contains(out, "[mcp_servers.semidx-dev]\ncommand = \"/dev\"") {
		t.Errorf("similarly named section clobbered:\n%s", out)
	}
}
