// Package pending persists, per project, the list of files that were skipped
// because they need a password, so `semidx unlock` can find them without
// re-walking. It stores ONLY file paths (never passwords) as JSON under
// <UserConfigDir>/semidx/pending/<key>.json with 0600 perms. The key is the
// project identity (git identity or "path:<abs>"), so it is stable across runs
// and independent of the relative path the user typed.
package pending

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// Registry is the per-project pending set.
type Registry struct {
	Project string   `json:"project"` // project name (for display / store lookup)
	Model   string   `json:"model"`   // model the project was indexed with
	Files   []string `json:"files"`   // ABSOLUTE paths of files awaiting a password
}

func dir() (string, error) {
	c, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(c, "semidx", "pending"), nil
}

// fileFor maps a project key to its registry file (a hash keeps the filename
// safe regardless of the key's characters).
func fileFor(key string) (string, error) {
	d, err := dir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(d, hex.EncodeToString(sum[:16])+".json"), nil
}

// Load returns the registry for a project key, or (nil, nil) when none exists.
func Load(key string) (*Registry, error) {
	p, err := fileFor(key)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Clean(p))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r Registry
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Save writes the registry (0600); an empty file list removes the registry.
func Save(key string, r *Registry) error {
	if r == nil || len(r.Files) == 0 {
		return Remove(key)
	}
	p, err := fileFor(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}

// Remove deletes a project's registry (absent is not an error).
func Remove(key string) error {
	p, err := fileFor(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
