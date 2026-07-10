package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAddGitleaksIgnore(t *testing.T) {
	d, err := NewDetector(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), ".gitleaksignore")
	if err := os.WriteFile(p, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.AddGitleaksIgnore(p); err != nil {
		t.Fatalf("AddGitleaksIgnore: %v", err)
	}
}

func TestAddGitleaksIgnoreNilDetector(t *testing.T) {
	var d *Detector
	if err := d.AddGitleaksIgnore("nonexistent"); err != nil {
		t.Fatalf("nil detector should no-op: %v", err)
	}
}

func TestClose(t *testing.T) {
	d, err := NewDetector(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d.Close() // must not panic
}
