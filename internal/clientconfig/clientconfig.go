// Package clientconfig persists how the CLI reaches a semidx server: the server
// URL, an API token, and a default project. It lives at
// $XDG_CONFIG_HOME/semidx/config.yaml (usually ~/.config/semidx/config.yaml) and
// is written by `semidx login`. Environment variables override the file so the
// same machine can point at a different server per-shell.
package clientconfig

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the on-disk client configuration.
type Config struct {
	ServerURL      string `yaml:"server_url"`
	Token          string `yaml:"token"`
	DefaultProject string `yaml:"default_project,omitempty"`
}

// Path returns the config file location, honoring XDG_CONFIG_HOME.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "semidx", "config.yaml"), nil
}

// Load reads the config file (a missing file is not an error — an empty Config is
// returned) and then applies environment overrides. SEMIDX_SERVER_URL,
// SEMIDX_TOKEN and SEMIDX_DEFAULT_PROJECT win over the file when set.
func Load() (*Config, error) {
	c := &Config{}
	p, err := Path()
	if err != nil {
		return nil, err
	}
	// #nosec G304 -- reads the CLI's own config file at a well-known per-user path.
	if data, err := os.ReadFile(p); err == nil {
		if err := yaml.Unmarshal(data, c); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if v := os.Getenv("SEMIDX_SERVER_URL"); v != "" {
		c.ServerURL = v
	}
	if v := os.Getenv("SEMIDX_TOKEN"); v != "" {
		c.Token = v
	}
	if v := os.Getenv("SEMIDX_DEFAULT_PROJECT"); v != "" {
		c.DefaultProject = v
	}
	return c, nil
}

// Save writes the config to disk with 0600 perms (it holds a token) under a 0700
// directory.
func Save(c *Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}
