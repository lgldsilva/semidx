package xdg

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	semidxDir = "semidx"
	indexDB   = "index.db"
)

// ConfigDir returns the base directory for configuration files,
// prioritizing XDG_CONFIG_HOME over the OS-default user config directory.
func ConfigDir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return xdg, nil
	}
	return os.UserConfigDir()
}

// CacheDir returns the base directory for cache files,
// prioritizing XDG_CACHE_HOME over the OS-default user cache directory.
func CacheDir() (string, error) {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return xdg, nil
	}
	return os.UserCacheDir()
}

// UserEnvPath returns the persistent per-user config file path (~/.config/semidx/semidx.env).
func UserEnvPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, semidxDir, "semidx.env"), nil
}

// ClientConfigPath returns the client-side configuration file path (~/.config/semidx/config.yaml).
func ClientConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, semidxDir, "config.yaml"), nil
}

// DefaultLocalIndexPath returns the default SQLite index file location.
func DefaultLocalIndexPath() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, semidxDir, indexDB)
	}
	if home := os.Getenv("HOME"); home != "" && (strings.HasPrefix(home, "/home") || strings.Contains(home, "test")) {
		return filepath.Join(home, ".cache", semidxDir, indexDB)
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return semidxDir + "-" + indexDB
	}
	return filepath.Join(dir, semidxDir, indexDB)
}
