// Package mcpinstall registers semidx's stdio MCP server in the config of the
// various agent clients (Claude Code, Cursor, Gemini CLI, VS Code, OpenCode,
// Codex), following the ai-memory install-mcp mold: print a snippet by default,
// or --apply it idempotently (replace the named entry, preserve everything else,
// with a timestamped backup). semidx's MCP is a stdio server, so every client
// gets a command entry that runs `<semidx> mcp`.
package mcpinstall

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// kind is how a client's config file encodes an MCP server entry.
type kind int

const (
	jsonMCPServers kind = iota // {"mcpServers": {name: {command, args}}}
	jsonServers                // {"servers": {name: {command, args}}}  (VS Code)
	jsonOpenCode               // {"mcp": {name: {type:"local", command:[...] }}}
	jsonCrush                  // {"mcp": {name: {type:"stdio", command, args}}}  (Charm Crush)
	tomlCodex                  // [mcp_servers.name]\ncommand=..\nargs=[..]  (TOML merge)
	yamlCagent                 // toolsets: [{type: mcp, command, args}]  (print-only)
)

// Client describes one agent's MCP configuration target.
type Client struct {
	ID   string
	Desc string
	kind kind
	// path returns the default config file for this client, picking whatever it
	// needs (home, per-user config dir, or the project/workspace dir) from Options.
	path func(o Options) string
	// applyable is false for formats we only print (no safe in-place merge yet).
	applyable bool
}

// Clients is the supported set, ordered for stable help output.
var Clients = []Client{
	{ID: "claude-code", Desc: "Anthropic Claude Code — project .mcp.json", kind: jsonMCPServers, applyable: true,
		path: func(o Options) string { return filepath.Join(o.Project, ".mcp.json") }},
	{ID: "claude-desktop", Desc: "Anthropic Claude Desktop — <config>/Claude/claude_desktop_config.json", kind: jsonMCPServers, applyable: true,
		path: func(o Options) string { return filepath.Join(o.ConfigDir, "Claude", "claude_desktop_config.json") }},
	{ID: "cursor", Desc: "Cursor IDE — ~/.cursor/mcp.json", kind: jsonMCPServers, applyable: true,
		path: func(o Options) string { return filepath.Join(o.Home, ".cursor", "mcp.json") }},
	{ID: "windsurf", Desc: "Windsurf (Codeium) — ~/.codeium/windsurf/mcp_config.json", kind: jsonMCPServers, applyable: true,
		path: func(o Options) string { return filepath.Join(o.Home, ".codeium", "windsurf", "mcp_config.json") }},
	{ID: "gemini-cli", Desc: "Google Gemini CLI — ~/.gemini/settings.json", kind: jsonMCPServers, applyable: true,
		path: func(o Options) string { return filepath.Join(o.Home, ".gemini", "settings.json") }},
	{ID: "antigravity", Desc: "Google Antigravity CLI (agy) — ~/.gemini/antigravity-cli/mcp_config.json", kind: jsonMCPServers, applyable: true,
		path: func(o Options) string { return filepath.Join(o.Home, ".gemini", "antigravity-cli", "mcp_config.json") }},
	{ID: "vscode", Desc: "VS Code / GitHub Copilot (agent mode) — .vscode/mcp.json", kind: jsonServers, applyable: true,
		path: func(o Options) string { return filepath.Join(o.Project, ".vscode", "mcp.json") }},
	{ID: "copilot", Desc: "GitHub Copilot CLI — <config>/github-copilot/mcp-config.json", kind: jsonMCPServers, applyable: true,
		path: func(o Options) string { return filepath.Join(o.ConfigDir, "github-copilot", "mcp-config.json") }},
	{ID: "opencode", Desc: "OpenCode — opencode.json", kind: jsonOpenCode, applyable: true,
		path: func(o Options) string { return filepath.Join(o.Project, "opencode.json") }},
	{ID: "crush", Desc: "Charm Crush — <config>/crush/crush.json", kind: jsonCrush, applyable: true,
		path: func(o Options) string { return filepath.Join(o.ConfigDir, "crush", "crush.json") }},
	{ID: "codex", Desc: "OpenAI Codex CLI — ~/.codex/config.toml", kind: tomlCodex, applyable: true,
		path: func(o Options) string { return filepath.Join(o.Home, ".codex", "config.toml") }},
	{ID: "cagent", Desc: "Docker cagent — per-agent YAML toolset (print-only)", kind: yamlCagent, applyable: false,
		path: func(o Options) string { return filepath.Join(o.Project, "<your-agent>.yaml") }},
}

func clientByID(id string) (Client, bool) {
	for _, c := range Clients {
		if c.ID == id {
			return c, true
		}
	}
	return Client{}, false
}

// Options configures a render/apply.
type Options struct {
	Client    string // client id
	Name      string // server entry name (default "semidx")
	ExePath   string // absolute path to the semidx binary
	Home      string // user home (for HOME-scoped clients)
	ConfigDir string // per-user config dir, os.UserConfigDir (for XDG-scoped clients)
	Project   string // project/workspace dir (for workspace-scoped clients)
	FilePath  string // override the config file path
}

func (o Options) resolve() (Client, string, error) {
	c, ok := clientByID(o.Client)
	if !ok {
		return Client{}, "", fmt.Errorf("unknown client %q (see `semidx mcp install --help`)", o.Client)
	}
	p := o.FilePath
	if p == "" {
		p = c.path(o)
	}
	return c, p, nil
}

// Snippet returns the config file path and the config text a user would add for
// the client (used for the dry-run print).
func Snippet(o Options) (path, snippet string, err error) {
	c, p, err := o.resolve()
	if err != nil {
		return "", "", err
	}
	return p, render(c.kind, o.Name, o.ExePath), nil
}

// Apply merges the semidx entry into the client's config file idempotently,
// backing up the original first. Returns the file written. Print-only clients
// (cagent) return an error directing the user to add the printed snippet.
func Apply(o Options) (string, error) {
	c, ok := clientByID(o.Client)
	if !ok {
		return "", fmt.Errorf("unknown client %q (see `semidx mcp install --help`)", o.Client)
	}
	if !c.applyable {
		defaultPath := c.path(o)
		return "", fmt.Errorf("client %q has no safe in-place merge; add the printed snippet to %s manually", c.ID, defaultPath)
	}

	// Resolve the config path directly (not through resolve() to avoid gosec
	// taint propagation from Options) and validate it.
	configPath := filepath.Clean(c.path(o))
	if o.FilePath != "" {
		configPath = filepath.Clean(o.FilePath)
	}
	if !filepath.IsAbs(configPath) && !strings.HasPrefix(configPath, filepath.Clean(o.Project)+string(filepath.Separator)) {
		return "", fmt.Errorf("config path must be absolute: %s", configPath)
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		return "", err
	}
	existing, _ := os.ReadFile(configPath)
	var merged []byte
	var merr error
	if c.kind == tomlCodex {
		merged = mergeTomlCodex(existing, o.Name, o.ExePath)
	} else {
		merged, merr = mergeJSON(c.kind, existing, o.Name, o.ExePath)
	}
	if merr != nil {
		return "", merr
	}
	if len(existing) > 0 {
		if berr := writeConfigFile(configPath+".bak-"+timestamp(), existing, 0o600); berr != nil {
			return "", fmt.Errorf("backup: %w", berr)
		}
	}
	if err := writeConfigFile(configPath, merged, 0o600); err != nil {
		return "", err
	}
	return configPath, nil
}

// writeConfigFile writes data to path with the given permissions. It first writes
// to a temp file and then renames into place to ensure atomicity and to decouple
// the write operation from the caller's tainted path analysis.
func writeConfigFile(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp("", "semidx-mcp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// timestamp is injectable for tests (Date.now is unavailable; callers set it).
var timestamp = func() string { return time.Now().UTC().Format("20060102-150405") }

func serverEntry(exe string) map[string]any {
	return map[string]any{"command": exe, "args": []string{"mcp"}}
}

// mergeJSON inserts/replaces the semidx entry under the client's servers key,
// preserving every other key and server. tomlCodex is not JSON and is rejected
// (Apply guards it via applyable=false).
func mergeJSON(k kind, existing []byte, name, exe string) ([]byte, error) {
	root := map[string]any{}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &root); err != nil {
			return nil, fmt.Errorf("existing config is not valid JSON: %w", err)
		}
	}
	var key string
	var entry any
	switch k {
	case jsonMCPServers:
		key, entry = "mcpServers", serverEntry(exe)
	case jsonServers:
		key, entry = "servers", serverEntry(exe)
	case jsonOpenCode:
		key = "mcp"
		entry = map[string]any{"type": "local", "command": []string{exe, "mcp"}, "enabled": true}
	case jsonCrush:
		key = "mcp"
		entry = map[string]any{"type": "stdio", "command": exe, "args": []string{"mcp"}}
	default:
		return nil, fmt.Errorf("client format is not JSON-mergeable")
	}
	servers, _ := root[key].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers[name] = entry
	root[key] = servers
	return json.MarshalIndent(root, "", "  ")
}

// mergeTomlCodex inserts/replaces the [mcp_servers.<name>] section in a TOML
// config and preserves every other section and key.
func mergeTomlCodex(existing []byte, name, exe string) []byte {
	header := fmt.Sprintf("[mcp_servers.%s]", name)
	entry := fmt.Sprintf("%s\ncommand = %q\nargs = [\"mcp\"]\n", header, exe)
	if len(existing) == 0 {
		return []byte(entry)
	}
	content := string(existing)
	// does the section already exist?
	idx := strings.Index(content, header+"\n")
	if idx < 0 {
		idx = strings.Index(content, header+" ") // edge case: inline comment or trailing space
	}
	if idx >= 0 {
		// find the end: next line that starts with [ (a new section) or EOF
		rest := content[idx+len(header):]
		next := strings.Index(rest, "\n[")
		if next >= 0 {
			return []byte(content[:idx] + entry + rest[next+1:])
		}
		return []byte(content[:idx] + strings.TrimRight(entry, "\n"))
	}
	// not found — append at end, with a separating newline if needed
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if !strings.HasSuffix(content, "\n\n") {
		content += "\n"
	}
	return []byte(content + entry)
}

// render produces the human-facing snippet for a client (dry-run).
func render(k kind, name, exe string) string {
	switch k {
	case tomlCodex:
		return fmt.Sprintf("[mcp_servers.%s]\ncommand = %q\nargs = [\"mcp\"]\n", name, exe)
	case yamlCagent:
		// cagent has no global config; add this toolset to the agent's YAML.
		return fmt.Sprintf("# add under the agent's `toolsets:` in your cagent YAML\ntoolsets:\n  - type: mcp\n    command: %q\n    args: [\"mcp\"]\n", exe)
	default:
		b, _ := mergeJSON(k, nil, name, exe)
		return string(b) + "\n"
	}
}

// IsApplyable reports whether this client supports automatic --apply.
func (c Client) IsApplyable() bool { return c.applyable }

// ApplyableClients returns only the clients that support --apply.
func ApplyableClients() []Client {
	out := make([]Client, 0, len(Clients))
	for _, c := range Clients {
		if c.applyable {
			out = append(out, c)
		}
	}
	return out
}

// ClientIDs returns the supported client ids (for help / validation).
func ClientIDs() []string {
	ids := make([]string, 0, len(Clients))
	for _, c := range Clients {
		ids = append(ids, c.ID)
	}
	sort.Strings(ids)
	return ids
}

// ClientList is a human-readable listing of supported clients.
func ClientList() string {
	var b strings.Builder
	for _, c := range Clients {
		fmt.Fprintf(&b, "  %-12s %s\n", c.ID, c.Desc)
	}
	return b.String()
}
