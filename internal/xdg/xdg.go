package xdg

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	semidxDir    = "semidx"
	indexDB      = "index.db"
	configDirEnv = "SEMIDX_CONFIG_DIR"
)

var (
	// activeProfile holds the currently selected config profile, if any.
	activeProfile string

	// profileNameRe validates profile names: letters, digits, dot, dash, underscore only.
	profileNameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

	// ErrInvalidProfile is returned when a profile name contains invalid characters.
	ErrInvalidProfile = errors.New("profile name must contain only letters, digits, dot, dash, or underscore")
)

// SetProfile sets the active config profile. An empty string or "default" clears
// the profile and uses the default config files. Returns ErrInvalidProfile for
// names containing characters outside [a-zA-Z0-9._-].
func SetProfile(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || name == "default" {
		activeProfile = ""
		return nil
	}
	if !profileNameRe.MatchString(name) {
		return ErrInvalidProfile
	}
	activeProfile = name
	return nil
}

// Profile returns the active profile name, or "" if no profile is selected.
func Profile() string { return activeProfile }

// semidxConfigDir returns the directory containing semidx config files.
// When SEMIDX_CONFIG_DIR is set that directory is used directly; otherwise
// it falls back to <ConfigDir>/semidx.
func semidxConfigDir() (string, error) {
	if d := os.Getenv(configDirEnv); d != "" {
		return d, nil
	}
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, semidxDir), nil
}

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

// UserEnvPath returns the persistent per-user config file path. When a profile
// is active, the path uses the naming convention <ConfigDir>/semidx-<profile>.env.
func UserEnvPath() (string, error) {
	dir, err := semidxConfigDir()
	if err != nil {
		return "", err
	}
	name := "semidx.env"
	if activeProfile != "" {
		name = "semidx-" + activeProfile + ".env"
	}
	return filepath.Join(dir, name), nil
}

// ClientConfigPath returns the client-side configuration file path. When a
// profile is active, the path uses <ConfigDir>/config-<profile>.yaml.
func ClientConfigPath() (string, error) {
	dir, err := semidxConfigDir()
	if err != nil {
		return "", err
	}
	name := "config.yaml"
	if activeProfile != "" {
		name = "config-" + activeProfile + ".yaml"
	}
	return filepath.Join(dir, name), nil
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
