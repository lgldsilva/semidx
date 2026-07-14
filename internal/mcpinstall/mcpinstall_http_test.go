package mcpinstall

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func httpOpts(client string) Options {
	return Options{
		Client: client, Name: "semidx",
		Home: "/home/u", ConfigDir: "/home/u/.config", Project: "/proj",
		Transport: TransportHTTP, URL: "https://semidx.lan/mcp", BearerEnv: "SEMIDX_TOKEN",
	}
}

// decodeEntry unmarshals a snippet and returns the semidx entry under key.
func decodeEntry(t *testing.T, snippet, key string) map[string]any {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal([]byte(snippet), &root); err != nil {
		t.Fatalf("snippet is not JSON: %v\n%s", err, snippet)
	}
	servers, _ := root[key].(map[string]any)
	if servers == nil {
		t.Fatalf("snippet missing %q key:\n%s", key, snippet)
	}
	entry, _ := servers["semidx"].(map[string]any)
	if entry == nil {
		t.Fatalf("snippet missing semidx entry:\n%s", snippet)
	}
	return entry
}

func headerOf(t *testing.T, entry map[string]any) string {
	t.Helper()
	headers, _ := entry["headers"].(map[string]any)
	if headers == nil {
		t.Fatalf("entry missing headers: %v", entry)
	}
	auth, _ := headers["Authorization"].(string)
	return auth
}

func TestSnippetHTTPPerClientShape(t *testing.T) {
	tests := []struct {
		client  string
		key     string
		urlKey  string
		typeVal string // "" = no type field expected
		auth    string
	}{
		{"claude-code", "mcpServers", "url", "http", "Bearer ${SEMIDX_TOKEN}"},
		{"cursor", "mcpServers", "url", "", "Bearer ${SEMIDX_TOKEN}"},
		{"gemini-cli", "mcpServers", "httpUrl", "", "Bearer $SEMIDX_TOKEN"},
		{"vscode", "servers", "url", "http", "Bearer ${env:SEMIDX_TOKEN}"},
		{"copilot", "mcpServers", "url", "http", "Bearer ${SEMIDX_TOKEN}"},
		{"opencode", "mcp", "url", "remote", "Bearer {env:SEMIDX_TOKEN}"},
		{"crush", "mcp", "url", "http", "Bearer $SEMIDX_TOKEN"},
	}
	for _, tt := range tests {
		t.Run(tt.client, func(t *testing.T) {
			_, snippet, err := Snippet(httpOpts(tt.client))
			if err != nil {
				t.Fatalf("Snippet: %v", err)
			}
			entry := decodeEntry(t, snippet, tt.key)
			if got, _ := entry[tt.urlKey].(string); got != "https://semidx.lan/mcp" {
				t.Errorf("%s = %q, want the /mcp URL", tt.urlKey, got)
			}
			if tt.typeVal != "" {
				if got, _ := entry["type"].(string); got != tt.typeVal {
					t.Errorf("type = %q, want %q", got, tt.typeVal)
				}
			}
			if got := headerOf(t, entry); got != tt.auth {
				t.Errorf("Authorization = %q, want %q", got, tt.auth)
			}
			// The token value itself must never appear — only the env reference.
			if strings.Contains(snippet, "command") {
				t.Errorf("http snippet must not contain a command entry:\n%s", snippet)
			}
		})
	}
}

func TestSnippetHTTPCodexTOML(t *testing.T) {
	_, snippet, err := Snippet(httpOpts("codex"))
	if err != nil {
		t.Fatalf("Snippet: %v", err)
	}
	for _, want := range []string{
		"[mcp_servers.semidx]",
		`url = "https://semidx.lan/mcp"`,
		`bearer_token_env_var = "SEMIDX_TOKEN"`,
	} {
		if !strings.Contains(snippet, want) {
			t.Errorf("codex TOML missing %q:\n%s", want, snippet)
		}
	}
	if strings.Contains(snippet, "command") {
		t.Errorf("codex http snippet must not have a command line:\n%s", snippet)
	}
}

func TestSnippetHTTPWithoutBearerEnvHasNoHeaders(t *testing.T) {
	o := httpOpts("claude-code")
	o.BearerEnv = ""
	_, snippet, err := Snippet(o)
	if err != nil {
		t.Fatalf("Snippet: %v", err)
	}
	if strings.Contains(snippet, "headers") || strings.Contains(snippet, "Authorization") {
		t.Errorf("no bearer env → no headers block:\n%s", snippet)
	}
	// Codex: no bearer_token_env_var line.
	o = httpOpts("codex")
	o.BearerEnv = ""
	_, snippet, err = Snippet(o)
	if err != nil {
		t.Fatalf("Snippet codex: %v", err)
	}
	if strings.Contains(snippet, "bearer_token_env_var") {
		t.Errorf("no bearer env → no bearer_token_env_var:\n%s", snippet)
	}
}

func TestHTTPUnsupportedClientsAreRejected(t *testing.T) {
	for _, client := range []string{"claude-desktop", "windsurf", "antigravity", "cagent"} {
		t.Run(client, func(t *testing.T) {
			if _, _, err := Snippet(httpOpts(client)); err == nil || !strings.Contains(err.Error(), "does not support HTTP") {
				t.Errorf("Snippet(%s, http) err = %v, want no-HTTP-support error", client, err)
			}
			if _, err := Apply(httpOpts(client)); err == nil || !strings.Contains(err.Error(), "does not support HTTP") {
				t.Errorf("Apply(%s, http) err = %v, want no-HTTP-support error", client, err)
			}
		})
	}
}

func TestHTTPTransportRequiresURL(t *testing.T) {
	o := httpOpts("claude-code")
	o.URL = ""
	if _, _, err := Snippet(o); err == nil || !strings.Contains(err.Error(), "--url") {
		t.Errorf("missing --url err = %v", err)
	}
}

func TestUnknownTransportRejected(t *testing.T) {
	o := httpOpts("claude-code")
	o.Transport = "carrier-pigeon"
	if _, _, err := Snippet(o); err == nil || !strings.Contains(err.Error(), "unknown transport") {
		t.Errorf("unknown transport err = %v", err)
	}
}

func TestApplyHTTPMergesPreservingOtherServers(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, ".mcp.json")
	seed := `{"mcpServers":{"other":{"command":"/bin/other","env":{"K":"V"}}}}`
	if err := os.WriteFile(cfg, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	o := httpOpts("claude-code")
	o.Project = dir
	written, err := Apply(o)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, err := os.ReadFile(written)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("merged config invalid: %v", err)
	}
	servers := root["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Error("merge dropped the pre-existing server")
	}
	entry, _ := servers["semidx"].(map[string]any)
	if entry == nil || entry["url"] != "https://semidx.lan/mcp" || entry["type"] != "http" {
		t.Errorf("semidx http entry = %v", entry)
	}
}

func TestApplyHTTPReplacesStdioEntry(t *testing.T) {
	dir := t.TempDir()
	// First install stdio, then switch the same entry to http.
	stdio := Options{Client: "claude-code", Name: "semidx", ExePath: "/bin/semidx", Project: dir}
	if _, err := Apply(stdio); err != nil {
		t.Fatalf("Apply stdio: %v", err)
	}
	o := httpOpts("claude-code")
	o.Project = dir
	written, err := Apply(o)
	if err != nil {
		t.Fatalf("Apply http: %v", err)
	}
	data, _ := os.ReadFile(written)
	if strings.Contains(string(data), "command") {
		t.Errorf("http apply should replace the stdio entry:\n%s", data)
	}
	if !strings.Contains(string(data), "https://semidx.lan/mcp") {
		t.Errorf("merged config missing the URL:\n%s", data)
	}
}

func TestApplyHTTPCodexTOMLReplacesStdioSection(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	seed := "[other]\nkey = \"v\"\n\n[mcp_servers.semidx]\ncommand = \"/bin/semidx\"\nargs = [\"mcp\"]\n"
	if err := os.WriteFile(cfg, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	o := httpOpts("codex")
	o.FilePath = cfg
	written, err := Apply(o)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(written)
	out := string(data)
	if !strings.Contains(out, "[other]") {
		t.Errorf("merge dropped the other section:\n%s", out)
	}
	if strings.Contains(out, "command") {
		t.Errorf("stdio section should be replaced:\n%s", out)
	}
	if !strings.Contains(out, `url = "https://semidx.lan/mcp"`) || !strings.Contains(out, `bearer_token_env_var = "SEMIDX_TOKEN"`) {
		t.Errorf("codex http section incomplete:\n%s", out)
	}
}

func TestSupportsHTTPMatchesClientTable(t *testing.T) {
	want := map[string]bool{
		"claude-code": true, "cursor": true, "gemini-cli": true, "vscode": true,
		"copilot": true, "opencode": true, "crush": true, "codex": true,
		"claude-desktop": false, "windsurf": false, "antigravity": false, "cagent": false,
	}
	for _, c := range Clients {
		if got := c.SupportsHTTP(); got != want[c.ID] {
			t.Errorf("SupportsHTTP(%s) = %v, want %v", c.ID, got, want[c.ID])
		}
	}
}

func TestHTTPEntryInternals(t *testing.T) {
	// Default env reference syntax when a client declares none.
	c := Client{}
	if got := c.bearerHeaders("TOK")["Authorization"]; got != "Bearer ${TOK}" {
		t.Errorf("default envRef header = %q, want Bearer ${TOK}", got)
	}
	if c.bearerHeaders("") != nil {
		t.Error("empty bearer env must yield no headers")
	}
	// httpUnsupported style yields no entry.
	if e := c.httpEntry("https://x/mcp", "TOK"); e != nil {
		t.Errorf("unsupported httpEntry = %v, want nil", e)
	}
	// Non-JSON kinds have no servers key.
	if _, err := jsonKey(yamlCagent); err == nil {
		t.Error("jsonKey(yamlCagent) must error")
	}
	if _, err := jsonKey(tomlCodex); err == nil {
		t.Error("jsonKey(tomlCodex) must error")
	}
}

func TestStdioSnippetUnchangedByTransportDefault(t *testing.T) {
	// Transport "" and "stdio" must render identically.
	a := Options{Client: "claude-code", Name: "semidx", ExePath: "/bin/semidx", Project: "/proj"}
	b := a
	b.Transport = TransportStdio
	_, sa, err := Snippet(a)
	if err != nil {
		t.Fatal(err)
	}
	_, sb, err := Snippet(b)
	if err != nil {
		t.Fatal(err)
	}
	if sa != sb {
		t.Errorf("default and explicit stdio snippets differ:\n%s\nvs\n%s", sa, sb)
	}
}
