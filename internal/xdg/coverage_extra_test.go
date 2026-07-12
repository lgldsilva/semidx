package xdg

import (
	"path/filepath"
	"testing"
)

func TestCacheDirXDG(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/tmp/xcache")
	got, err := CacheDir()
	if err != nil || got != "/tmp/xcache" {
		t.Fatalf("CacheDir = %q err=%v", got, err)
	}
}

func TestCacheDirDefault(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	got, err := CacheDir()
	if err != nil || got == "" {
		t.Fatalf("CacheDir default = %q err=%v", got, err)
	}
}

func TestDefaultLocalIndexPathXDG(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/tmp/xcache")
	got := DefaultLocalIndexPath()
	want := filepath.Join("/tmp/xcache", semidxDir, indexDB)
	if got != want {
		t.Fatalf("DefaultLocalIndexPath = %q want %q", got, want)
	}
}

func TestDefaultLocalIndexPathHome(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "/home/tester")
	got := DefaultLocalIndexPath()
	want := filepath.Join("/home/tester", ".cache", semidxDir, indexDB)
	if got != want {
		t.Fatalf("DefaultLocalIndexPath = %q want %q", got, want)
	}
}
