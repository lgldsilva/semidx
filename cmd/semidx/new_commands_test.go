package main

import (
	"os"
	"testing"

	"github.com/lgldsilva/semidx/internal/deadcode"
)

func TestParseFileLine(t *testing.T) {
	tests := []struct {
		input    string
		wantOK   bool
		wantFile string
		wantLine int
	}{
		{"internal/auth/token.go:42", true, "internal/auth/token.go", 42},
		{"file.go:1", true, "file.go", 1},
		{"path/to/file.go:999", true, "path/to/file.go", 999},
		{"no-colon", false, "", 0},
		{"file.go:abc", false, "", 0},
		{"file.go:0", false, "", 0},
		{":42", true, "", 42},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseFileLine(tt.input)
			if tt.wantOK {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got.File != tt.wantFile {
					t.Errorf("File = %q, want %q", got.File, tt.wantFile)
				}
				if got.Line != tt.wantLine {
					t.Errorf("Line = %d, want %d", got.Line, tt.wantLine)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
			}
		})
	}
}

func TestPrintDeadCodeResults_Empty(t *testing.T) {
	// Just ensure it doesn't panic with no findings.
	printDeadCodeResults(nil, deadcode.Stats{})
}

func TestPrintDeadCodeResults_WithFindings(t *testing.T) {
	findings := []deadcode.Finding{
		{Symbol: "parseV1", Kind: "func", File: "internal/old.go", StartLine: 45, Confidence: "confirmed"},
		{Symbol: "NewClient", Kind: "func", File: "pkg/public/exp.go", StartLine: 34, Confidence: "public-api"},
		{Symbol: "FormatV1", Kind: "func", File: "internal/old.go", StartLine: 89, Confidence: "confirmed"},
	}
	stats := deadcode.AggregateStats(findings)
	// Just ensure it doesn't panic.
	printDeadCodeResults(findings, stats)
}

func TestGoPackageName(t *testing.T) {
	tests := []struct {
		content string
		want    string
	}{
		{`package main`, "main"},
		{`package auth`, "auth"},
		{"package auth\n\nimport \"fmt\"", "auth"},
		{"// comment\npackage auth", "auth"},
		{"no package line here", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := goPackageName([]byte(tt.content))
			if got != tt.want {
				t.Errorf("goPackageName(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}

func TestDetectModulePath(t *testing.T) {
	// Test with a temp go.mod file.
	tmpDir := t.TempDir()
	gomod := []byte("module github.com/test/module\n\ngo 1.25.0\n")
	if err := writeFile(tmpDir+"/go.mod", gomod, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectModulePath(tmpDir); got != "github.com/test/module" {
		t.Errorf("detectModulePath = %q, want %q", got, "github.com/test/module")
	}
}

func TestDetectModulePath_NoFile(t *testing.T) {
	if got := detectModulePath(t.TempDir()); got != "" {
		t.Errorf("detectModulePath = %q, want \"\"", got)
	}
}

func TestFindTestFiles(t *testing.T) {
	tmpDir := t.TempDir()
	srcDir := tmpDir + "/internal/auth"
	if err := mkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a source file.
	_ = writeFile(srcDir+"/token.go", []byte("package auth\nfunc ValidateToken() {}"), 0o644)

	// Create a test file that references the symbol.
	_ = writeFile(srcDir+"/token_test.go", []byte("package auth\nimport \"testing\"\nfunc TestValidateToken(t *testing.T) {\n\tValidateToken()\n}"), 0o644)

	// Create a test file that does NOT reference the symbol.
	_ = writeFile(srcDir+"/other_test.go", []byte("package auth\nimport \"testing\"\nfunc TestOther(t *testing.T) {}"), 0o644)

	results := findTestFiles(tmpDir, "internal/auth/token.go", "ValidateToken")
	if len(results) != 1 {
		t.Fatalf("expected 1 test file, got %d: %v", len(results), results)
	}
	if results[0] != "internal/auth/token_test.go" {
		t.Errorf("expected token_test.go, got %q", results[0])
	}
}

func TestFindTestFiles_NoTestDir(t *testing.T) {
	results := findTestFiles("/nonexistent", "foo.go", "SomeSymbol")
	if results != nil {
		t.Errorf("expected nil, got %v", results)
	}
}

// writeFile is a small helper to avoid importing os/filepath in test helpers.
func writeFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

func mkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}
