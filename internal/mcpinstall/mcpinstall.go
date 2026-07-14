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

// httpStyle is how a client's config encodes a REMOTE (Streamable HTTP) MCP
// server entry. httpUnsupported clients get a warning instead of a config.
type httpStyle int

const (
	httpUnsupported httpStyle = iota
	httpTypeHTTP              // {"type":"http","url":…,"headers":{…}}
	httpURLHeaders            // {"url":…,"headers":{…}}  (Cursor)
	httpGeminiURL             // {"httpUrl":…,"headers":{…}}  (Gemini CLI)
	httpOpenCode              // {"type":"remote","url":…,"headers":{…}}
	httpCodexTOML             // url = …, bearer_token_env_var = …
)

// Transport values for Options.Transport.
const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
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
	// httpKind is how this client encodes a remote Streamable-HTTP MCP server;
	// httpUnsupported means stdio-only (install --transport http warns/errors).
	httpKind httpStyle
	// envRef is the fmt verb rendering the client's env-variable reference
	// syntax inside config values (e.g. "${%s}"). Empty means "${%s}".
	envRef string
}

// Clients is the supported set, ordered for stable help output.
var Clients = []Client{
	{ID: "claude-code", Desc: "Anthropic Claude Code — project .mcp.json", kind: jsonMCPServers, applyable: true,
		httpKind: httpTypeHTTP, envRef: "${%s}",
		path: func(o Options) string { return filepath.Join(o.Project, ".mcp.json") }},
	{ID: "claude-desktop", Desc: "Anthropic Claude Desktop — <config>/Claude/claude_desktop_config.json", kind: jsonMCPServers, applyable: true,
		path: func(o Options) string { return filepath.Join(o.ConfigDir, "Claude", "claude_desktop_config.json") }},
	{ID: "cursor", Desc: "Cursor IDE — ~/.cursor/mcp.json", kind: jsonMCPServers, applyable: true,
		httpKind: httpURLHeaders, envRef: "${%s}",
		path: func(o Options) string { return filepath.Join(o.Home, ".cursor", "mcp.json") }},
	{ID: "windsurf", Desc: "Windsurf (Codeium) — ~/.codeium/windsurf/mcp_config.json", kind: jsonMCPServers, applyable: true,
		path: func(o Options) string { return filepath.Join(o.Home, ".codeium", "windsurf", "mcp_config.json") }},
	{ID: "gemini-cli", Desc: "Google Gemini CLI — ~/.gemini/settings.json", kind: jsonMCPServers, applyable: true,
		httpKind: httpGeminiURL, envRef: "$%s",
		path: func(o Options) string { return filepath.Join(o.Home, ".gemini", "settings.json") }},
	{ID: "antigravity", Desc: "Google Antigravity CLI (agy) — ~/.gemini/antigravity-cli/mcp_config.json", kind: jsonMCPServers, applyable: true,
		path: func(o Options) string { return filepath.Join(o.Home, ".gemini", "antigravity-cli", "mcp_config.json") }},
	{ID: "vscode", Desc: "VS Code / GitHub Copilot (agent mode) — .vscode/mcp.json", kind: jsonServers, applyable: true,
		httpKind: httpTypeHTTP, envRef: "${env:%s}",
		path: func(o Options) string { return filepath.Join(o.Project, ".vscode", "mcp.json") }},
	{ID: "copilot", Desc: "GitHub Copilot CLI — <config>/github-copilot/mcp-config.json", kind: jsonMCPServers, applyable: true,
		httpKind: httpTypeHTTP, envRef: "${%s}",
		path: func(o Options) string { return filepath.Join(o.ConfigDir, "github-copilot", "mcp-config.json") }},
	{ID: "opencode", Desc: "OpenCode — opencode.json", kind: jsonOpenCode, applyable: true,
		httpKind: httpOpenCode, envRef: "{env:%s}",
		path: func(o Options) string { return filepath.Join(o.Project, "opencode.json") }},
	{ID: "crush", Desc: "Charm Crush — <config>/crush/crush.json", kind: jsonCrush, applyable: true,
		httpKind: httpTypeHTTP, envRef: "$%s",
		path: func(o Options) string { return filepath.Join(o.ConfigDir, "crush", "crush.json") }},
	{ID: "codex", Desc: "OpenAI Codex CLI — ~/.codex/config.toml", kind: tomlCodex, applyable: true,
		httpKind: httpCodexTOML,
		path:     func(o Options) string { return filepath.Join(o.Home, ".codex", "config.toml") }},
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
	Client    string            // client id
	Name      string            // server entry name (default "semidx")
	ExePath   string            // absolute path to the semidx binary
	Home      string            // user home (for HOME-scoped clients)
	ConfigDir string            // per-user config dir, os.UserConfigDir (for XDG-scoped clients)
	Project   string            // project/workspace dir (for workspace-scoped clients)
	FilePath  string            // override the config file path
	Env       map[string]string // extra environment variables for the server entry (stdio only)
	Transport string            // "stdio" (default) or "http" (remote Streamable HTTP server)
	URL       string            // the server's /mcp endpoint (http transport)
	BearerEnv string            // env var name holding the API token for the Authorization header (http transport)
}

// httpTransport reports whether the options ask for the remote HTTP transport.
func (o Options) httpTransport() bool { return o.Transport == TransportHTTP }

// validateTransport rejects an unknown transport, an http transport without a
// URL, and an http transport on a stdio-only client (the caller surfaces this
// as the per-client warning).
func (o Options) validateTransport(c Client) error {
	switch o.Transport {
	case "", TransportStdio:
		return nil
	case TransportHTTP:
		if o.URL == "" {
			return fmt.Errorf("http transport requires --url (the server's /mcp endpoint)")
		}
		if c.httpKind == httpUnsupported {
			return fmt.Errorf("client %q does not support HTTP/SSE MCP servers; use the stdio transport", c.ID)
		}
		return nil
	default:
		return fmt.Errorf("unknown transport %q (want %s or %s)", o.Transport, TransportStdio, TransportHTTP)
	}
}

func (o Options) resolve() (Client, string, error) {
	c, ok := clientByID(o.Client)
	if !ok {
		return Client{}, "", fmt.Errorf("unknown client %q (see `semidx mcp install --help`)", o.Client)
	}
	if err := o.validateTransport(c); err != nil {
		return Client{}, "", err
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
	return p, renderFor(c, o), nil
}

// Apply merges the semidx entry into the client's config file idempotently,
// backing up the original first. Returns the file written. Print-only clients
// (cagent) return an error directing the user to add the printed snippet.
func Apply(o Options) (string, error) {
	c, ok := clientByID(o.Client)
	if !ok {
		return "", fmt.Errorf("unknown client %q (see `semidx mcp install --help`)", o.Client)
	}
	if err := o.validateTransport(c); err != nil {
		return "", err
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
	merged, merr := mergeConfig(c, o, existing)
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

// mergeConfig merges the entry the options describe (stdio or http) into the
// client's existing config bytes.
func mergeConfig(c Client, o Options, existing []byte) ([]byte, error) {
	if c.kind == tomlCodex {
		if o.httpTransport() {
			return mergeTomlSection(existing, fmt.Sprintf("[mcp_servers.%s]", o.Name),
				tomlCodexHTTPEntry(o.Name, o.URL, o.BearerEnv)), nil
		}
		return mergeTomlCodex(existing, o.Name, o.ExePath, o.Env), nil
	}
	if o.httpTransport() {
		return mergeJSONEntry(c.kind, existing, o.Name, c.httpEntry(o.URL, o.BearerEnv))
	}
	return mergeJSON(c.kind, existing, o.Name, o.ExePath, o.Env)
}

// SupportsHTTP reports whether this client's config format can point at a
// remote Streamable HTTP MCP server (install --transport http).
func (c Client) SupportsHTTP() bool { return c.httpKind != httpUnsupported }

// bearerHeaders renders the Authorization header for the http transport using
// the client's env-variable reference syntax, so the token itself never lands
// in the config file. nil when no bearer env var was given.
func (c Client) bearerHeaders(bearerEnv string) map[string]string {
	if bearerEnv == "" {
		return nil
	}
	ref := c.envRef
	if ref == "" {
		ref = "${%s}"
	}
	return map[string]string{"Authorization": "Bearer " + fmt.Sprintf(ref, bearerEnv)}
}

// httpEntry builds the client's remote (Streamable HTTP) MCP server entry.
// Returns nil for stdio-only clients (validateTransport rejects those first).
func (c Client) httpEntry(url, bearerEnv string) map[string]any {
	var e map[string]any
	switch c.httpKind {
	case httpTypeHTTP:
		e = map[string]any{"type": "http", "url": url}
	case httpURLHeaders:
		e = map[string]any{"url": url}
	case httpGeminiURL:
		e = map[string]any{"httpUrl": url}
	case httpOpenCode:
		e = map[string]any{"type": "remote", "url": url, "enabled": true}
	default:
		return nil
	}
	if headers := c.bearerHeaders(bearerEnv); headers != nil {
		e["headers"] = headers
	}
	return e
}

// tomlCodexHTTPEntry renders the Codex TOML section for a remote server; Codex
// reads the bearer token natively from bearer_token_env_var.
func tomlCodexHTTPEntry(name, url, bearerEnv string) string {
	entry := fmt.Sprintf("[mcp_servers.%s]\nurl = %q\n", name, url)
	if bearerEnv != "" {
		entry += fmt.Sprintf("bearer_token_env_var = %q\n", bearerEnv)
	}
	return entry
}

func serverEntry(exe string, env map[string]string) map[string]any {
	e := map[string]any{"command": exe, "args": []string{"mcp"}}
	if len(env) > 0 {
		e["env"] = env
	}
	return e
}

// EnvKeys returns the env map's keys in sorted order, so every rendered format
// (JSON already sorts on marshal; TOML/YAML are built by hand) is deterministic.
func EnvKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ParseEnv parses repeated --env KEY=VAL values into a map. The value may
// contain '='; a missing '=' or an empty key is an error.
func ParseEnv(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	env := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid --env %q: want KEY=VAL", p)
		}
		env[k] = v
	}
	return env, nil
}

// LooksLikeSecret reports whether an env key name suggests a credential, so
// callers can warn that its value will land in a plain-text config file.
func LooksLikeSecret(key string) bool {
	k := strings.ToLower(key)
	for _, w := range []string{"key", "token", "secret", "password"} {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

// jsonKey returns the top-level key under which a JSON client stores its MCP
// servers. tomlCodex/yamlCagent are not JSON and are rejected.
func jsonKey(k kind) (string, error) {
	switch k {
	case jsonMCPServers:
		return "mcpServers", nil
	case jsonServers:
		return "servers", nil
	case jsonOpenCode, jsonCrush:
		return "mcp", nil
	default:
		return "", fmt.Errorf("client format is not JSON-mergeable")
	}
}

// stdioJSONEntry builds the client's stdio server entry.
func stdioJSONEntry(k kind, exe string, env map[string]string) any {
	switch k {
	case jsonOpenCode:
		oc := map[string]any{"type": "local", "command": []string{exe, "mcp"}, "enabled": true}
		if len(env) > 0 {
			oc["environment"] = env // opencode.json names the env block "environment"
		}
		return oc
	case jsonCrush:
		cr := map[string]any{"type": "stdio", "command": exe, "args": []string{"mcp"}}
		if len(env) > 0 {
			cr["env"] = env
		}
		return cr
	default:
		return serverEntry(exe, env)
	}
}

// mergeJSON inserts/replaces the semidx stdio entry under the client's servers
// key, preserving every other key and server (including their env blocks).
func mergeJSON(k kind, existing []byte, name, exe string, env map[string]string) ([]byte, error) {
	return mergeJSONEntry(k, existing, name, stdioJSONEntry(k, exe, env))
}

// mergeJSONEntry inserts/replaces an already-built entry under the client's
// servers key, preserving everything else in the file.
func mergeJSONEntry(k kind, existing []byte, name string, entry any) ([]byte, error) {
	key, err := jsonKey(k)
	if err != nil {
		return nil, err
	}
	root := map[string]any{}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &root); err != nil {
			return nil, fmt.Errorf("existing config is not valid JSON: %w", err)
		}
	}
	servers, _ := root[key].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers[name] = entry
	root[key] = servers
	return json.MarshalIndent(root, "", "  ")
}

// tomlEnvLine renders `env = { K = "V", … }` with sorted keys, or "" when empty.
// Keys that are not TOML bare keys ([A-Za-z0-9_-]) are quoted.
func tomlEnvLine(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	parts := make([]string, 0, len(env))
	for _, k := range EnvKeys(env) {
		parts = append(parts, fmt.Sprintf("%s = %q", tomlKey(k), env[k]))
	}
	return "env = { " + strings.Join(parts, ", ") + " }\n"
}

func tomlKey(k string) string {
	for _, r := range k {
		bare := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if !bare {
			return fmt.Sprintf("%q", k)
		}
	}
	return k
}

// mergeTomlCodex inserts/replaces the [mcp_servers.<name>] stdio section in a
// TOML config and preserves every other section and key.
func mergeTomlCodex(existing []byte, name, exe string, env map[string]string) []byte {
	header := fmt.Sprintf("[mcp_servers.%s]", name)
	entry := fmt.Sprintf("%s\ncommand = %q\nargs = [\"mcp\"]\n%s", header, exe, tomlEnvLine(env))
	return mergeTomlSection(existing, header, entry)
}

// mergeTomlSection inserts/replaces one TOML section (entry must start with
// header) and preserves every other section and key.
func mergeTomlSection(existing []byte, header, entry string) []byte {
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

// renderFor produces the human-facing snippet for the options (dry-run),
// dispatching between the stdio and http transports.
func renderFor(c Client, o Options) string {
	if !o.httpTransport() {
		return render(c.kind, o.Name, o.ExePath, o.Env)
	}
	if c.kind == tomlCodex {
		return tomlCodexHTTPEntry(o.Name, o.URL, o.BearerEnv)
	}
	out, _ := mergeJSONEntry(c.kind, nil, o.Name, c.httpEntry(o.URL, o.BearerEnv))
	return string(out) + "\n"
}

// render produces the human-facing stdio snippet for a client (dry-run).
func render(k kind, name, exe string, env map[string]string) string {
	switch k {
	case tomlCodex:
		return fmt.Sprintf("[mcp_servers.%s]\ncommand = %q\nargs = [\"mcp\"]\n%s", name, exe, tomlEnvLine(env))
	case yamlCagent:
		// cagent has no global config; add this toolset to the agent's YAML.
		var b strings.Builder
		fmt.Fprintf(&b, "# add under the agent's `toolsets:` in your cagent YAML\ntoolsets:\n  - type: mcp\n    command: %q\n    args: [\"mcp\"]\n", exe)
		if len(env) > 0 {
			b.WriteString("    env:\n")
			for _, key := range EnvKeys(env) {
				fmt.Fprintf(&b, "      %s: %q\n", key, env[key])
			}
		}
		return b.String()
	default:
		out, _ := mergeJSON(k, nil, name, exe, env)
		return string(out) + "\n"
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
