package xdg

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestDefaultLocalIndexPathUserCacheDirFallback covers the branch where neither
// XDG_CACHE_HOME nor a "/home"-style HOME is set, so the path falls through to
// os.UserCacheDir().
func TestDefaultLocalIndexPathUserCacheDirFallback(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	// A HOME that is neither under /home nor contains "test" forces the fallback
	// past the HOME shortcut and into os.UserCacheDir() (which itself reads HOME).
	t.Setenv("HOME", "/opt/xdgfallback")

	got := DefaultLocalIndexPath()
	if got == "" {
		t.Fatal("DefaultLocalIndexPath returned empty")
	}
	if filepath.Base(got) != indexDB {
		t.Fatalf("path %q does not end in %q", got, indexDB)
	}
	if !strings.Contains(got, semidxDir) {
		t.Fatalf("path %q missing %q segment", got, semidxDir)
	}
}

// TestUserEnvAndClientConfigPath_NoProfile covers the non-profile naming branch
// of UserEnvPath/ClientConfigPath (plain semidx.env / config.yaml).
func TestUserEnvAndClientConfigPath_NoProfile(t *testing.T) {
	p := saveProfile()
	defer restoreProfile(p)
	activeProfile = ""
	tmp := t.TempDir()
	t.Setenv(configDirEnv, tmp)

	env, err := UserEnvPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(env) != "semidx.env" {
		t.Fatalf("UserEnvPath base = %q, want semidx.env", filepath.Base(env))
	}
	cfg, err := ClientConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(cfg) != "config.yaml" {
		t.Fatalf("ClientConfigPath base = %q, want config.yaml", filepath.Base(cfg))
	}
}
