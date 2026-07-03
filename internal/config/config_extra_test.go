package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultLocalIndexPath(t *testing.T) {
	t.Run("honors XDG_DATA_HOME", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "/data")
		if got, want := DefaultLocalIndexPath(), filepath.Join("/data", "semidx", "index.db"); got != want {
			t.Errorf("DefaultLocalIndexPath() = %q, want %q", got, want)
		}
	})

	t.Run("falls back to the home directory", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "")
		t.Setenv("HOME", "/home/tester")
		want := filepath.Join("/home/tester", ".local", "share", "semidx", "index.db")
		if got := DefaultLocalIndexPath(); got != want {
			t.Errorf("DefaultLocalIndexPath() = %q, want %q", got, want)
		}
	})

	t.Run("uses a relative name when no home is resolvable", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "")
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
