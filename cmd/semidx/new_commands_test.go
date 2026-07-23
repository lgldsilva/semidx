package main

import (
	"testing"

	"github.com/lgldsilva/semidx/internal/codeintel"
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
			got, err := codeintel.ParseFileLine(tt.input)
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
