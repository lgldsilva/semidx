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

func TestClone(t *testing.T) {
	orig := &Config{GeminiAPIKey: "g", OllamaURLs: []string{"http://a", "http://b"}}
	cp := orig.Clone()
	if cp.GeminiAPIKey != "g" || !slicesEqual(cp.OllamaURLs, orig.OllamaURLs) {
		t.Errorf("Clone() = %+v, want a copy of %+v", cp, orig)
	}
	// Mutating the clone's slice must not affect the original.
	cp.OllamaURLs[0] = "http://mutated"
	if orig.OllamaURLs[0] != "http://a" {
		t.Error("Clone shares the OllamaURLs backing array with the original")
	}
	// A nil slice stays nil (no spurious allocation).
	if cp := (&Config{}).Clone(); cp.OllamaURLs != nil {
		t.Errorf("Clone of nil OllamaURLs = %v, want nil", cp.OllamaURLs)
	}
}

func TestFloatDefault(t *testing.T) {
	cases := []struct {
		in   string
		def  float64
		want float64
	}{
		{"", 0.3, 0.3},         // empty → default
		{"0", 0.3, 0},          // explicit zero is honored
		{"-1.5", 0.3, -1.5},    // negatives are explicit values
		{"0.7", 0.3, 0.7},      // plain parse
		{"nonsense", 0.3, 0.3}, // invalid → default
	}
	for _, tc := range cases {
		if got := floatDefault(tc.in, tc.def); got != tc.want {
			t.Errorf("floatDefault(%q, %v) = %v, want %v", tc.in, tc.def, got, tc.want)
		}
	}
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
