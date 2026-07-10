package secrets

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestScanConcurrentIgnore reproduces the data race where concurrent file
// workers call Scan (hence isIgnored) with distinct ignore-matching paths,
// each writing a new key into the ignoreSet cache. Before guarding ignoreSet
// with a mutex this panicked with "concurrent map writes" (and is flagged by
// the race detector). Run with -race.
func TestScanConcurrentIgnore(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ignoreFile), []byte("*.gen.go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := NewDetector(dir)
	if err != nil {
		t.Fatal(err)
	}

	const workers = 32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Distinct matching paths -> distinct cache keys -> concurrent
			// writes into ignoreSet. The path is ignored, so Scan short-circuits
			// before inspecting content; the payload is intentionally innocuous.
			path := fmt.Sprintf("pkg%d/file%d.gen.go", i, i)
			if got := d.Scan(path, []byte("package generated\n")); got != nil {
				t.Errorf("ignored path %q returned findings: %v", path, got)
			}
		}(i)
	}
	wg.Wait()
}
