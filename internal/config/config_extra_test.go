package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultLocalIndexPath(t *testing.T) {
	// The path uses the OS-native per-user cache dir (os.UserCacheDir), which is
	// cross-platform; on Linux it honors XDG_CACHE_HOME, else ~/.cache.
	t.Run("honors the cache dir", func(t *testing.T) {
		t.Setenv("XDG_CACHE_HOME", "/data")
		if got, want := DefaultLocalIndexPath(), filepath.Join("/data", "semidx", "index.db"); got != want {
			t.Errorf("DefaultLocalIndexPath() = %q, want %q", got, want)
		}
	})

	t.Run("falls back to the home cache directory", func(t *testing.T) {
		t.Setenv("XDG_CACHE_HOME", "")
		t.Setenv("HOME", "/home/tester")
		// os.UserCacheDir returns ~/Library/Caches on macOS, ~/.cache on Linux.
		// But HOME=/home/tester triggers the test-detection shortcut in xdg.go
		// (strings.Contains(home, "test")), which returns $HOME/.cache regardless of OS.
		want := filepath.Join("/home/tester", ".cache", "semidx", "index.db")
		if got := DefaultLocalIndexPath(); got != want {
			t.Errorf("DefaultLocalIndexPath() = %q, want %q", got, want)
		}
	})

	t.Run("uses a relative name when no cache dir is resolvable", func(t *testing.T) {
		t.Setenv("XDG_CACHE_HOME", "")
		t.Setenv("HOME", "")
		if got := DefaultLocalIndexPath(); got != "semidx-index.db" {
			t.Errorf("DefaultLocalIndexPath() = %q, want fallback semidx-index.db", got)
		}
	})
}

// TestFirstResolvesLegacyKeyFromDotEnv covers resolver.first's lowest branch:
// the legacy key present only in the .env file.
func TestFirstResolvesLegacyKeyFromDotEnv(t *testing.T) {
	clearEnv(t)
	writeDotEnv(t, "OLLAMA_URL=http://legacy-file:11434\n")
	if got := Load().OllamaURL; got != "http://legacy-file:11434" {
		t.Errorf("OllamaURL = %q, want the legacy key from .env", got)
	}
}
