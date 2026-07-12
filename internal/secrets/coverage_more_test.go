package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

// TestScanNilDetector covers the nil-safe guard in Scan.
func TestScanNilDetector(t *testing.T) {
	var d *Detector
	if got := d.Scan("x.go", []byte("whatever")); got != nil {
		t.Fatalf("nil detector Scan = %v, want nil", got)
	}
}

// TestScanIgnoredPath covers the isIgnored short-circuit inside Scan: a path
// matching a .semidx-secrets-ignore pattern is skipped before detection runs.
func TestScanIgnoredPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".semidx-secrets-ignore"), []byte("testdata/*\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := NewDetector(root)
	if err != nil {
		t.Fatal(err)
	}
	// A real AWS-looking key would normally be flagged; being under testdata/ it
	// must be skipped entirely.
	content := []byte("aws_secret_access_key = AKIAIOSFODNN7EXAMPLEAKIAIOSFODNN7")
	if got := d.Scan("testdata/creds.txt", content); got != nil {
		t.Fatalf("ignored path Scan = %v, want nil", got)
	}
	// And the memoised path is still ignored on a second call (cache hit branch).
	if got := d.Scan("testdata/creds.txt", content); got != nil {
		t.Fatalf("cached ignored path Scan = %v, want nil", got)
	}
}

// TestNewDetectorIgnoreFileSkipsCommentsAndBlanks covers the comment/blank-line
// skip in the ignore-file parser: only real patterns are loaded.
func TestNewDetectorIgnoreFileSkipsCommentsAndBlanks(t *testing.T) {
	root := t.TempDir()
	body := "# a comment\n\n   \nvendor/*\n"
	if err := os.WriteFile(filepath.Join(root, ".semidx-secrets-ignore"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := NewDetector(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.ignores) != 1 || d.ignores[0] != "vendor/*" {
		t.Fatalf("ignores = %v, want exactly [vendor/*]", d.ignores)
	}
}

// TestScanInlinePrecedingLine covers hasInlineIgnore's "annotation on the line
// before the finding" branch (as opposed to the same-line case already tested).
func TestScanInlinePrecedingLine(t *testing.T) {
	d, err := NewDetector(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The annotation sits on the line immediately above the secret, so
	// hasInlineIgnore's preceding-line branch must suppress any finding.
	content := []byte("package x\n// semidx:ignore-secret\nconst k = \"AKIAIOSFODNN7EXAMPLE\"\n")
	if got := d.Scan("main.go", content); len(got) != 0 {
		t.Fatalf("preceding-line annotation should suppress; got %d findings: %+v", len(got), got)
	}
}
