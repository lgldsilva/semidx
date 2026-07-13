package mcpclient

import (
	"encoding/json"
	"fmt"
	"os"
)

// fileConfig is the on-disk JSON shape: {"servers": [ServerConfig, ...]}. The
// object wrapper (rather than a bare array) leaves room for future top-level
// options without breaking the format.
type fileConfig struct {
	Servers []ServerConfig `json:"servers"`
}

// LoadConfig reads a JSON file describing external MCP servers. An empty path
// returns (nil, nil) so callers can treat "unconfigured" as "no servers". The
// JSON uses lowercase keys matching ServerConfig fields, e.g.:
//
//	{"servers": [
//	  {"name": "github", "transport": "stdio", "command": "gh-mcp", "enabled": true},
//	  {"name": "docs", "transport": "http", "url": "https://mcp.example/", "enabled": true}
//	]}
func LoadConfig(path string) ([]ServerConfig, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is an operator-provided config file, not user input
	if err != nil {
		return nil, fmt.Errorf("mcpclient: read config %q: %w", path, err)
	}
	var fc fileConfig
	if err := json.Unmarshal(data, &fc); err != nil {
		return nil, fmt.Errorf("mcpclient: parse config %q: %w", path, err)
	}
	return fc.Servers, nil
}
