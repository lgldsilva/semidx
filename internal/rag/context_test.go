package rag

import (
	"testing"
)

func TestDiversify(t *testing.T) {
	t.Parallel()

	sources := []Source{
		{File: "projA/main.go", StartLine: 1, Score: 0.9},
		{File: "projA/main.go", StartLine: 10, Score: 0.8},
		{File: "projA/main.go", StartLine: 20, Score: 0.7},
		{File: "projA/utils.go", StartLine: 1, Score: 0.6},
		{File: "projB/app.go", StartLine: 1, Score: 0.5},
		{File: "projB/app.go", StartLine: 15, Score: 0.4},
	}

	t.Run("cap per file", func(t *testing.T) {
		t.Parallel()
		got := diversify(sources, 2, 0)
		// projA/main.go: max 2 entries -> 2 of 3 kept
		// projA/utils.go: 1 entry
		// projB/app.go: max 2 entries -> kept both
		if len(got) != 5 {
			t.Errorf("diversify(maxPerFile=2) = %d, want 5 (projA/main.go cut from 3 to 2)", len(got))
		}
		// Verify order: first 2 projA/main.go, then projA/utils.go, then 2 projB/app.go
		if len(got) >= 1 && got[0].File != "projA/main.go" {
			t.Errorf("first entry should be projA/main.go, got %s", got[0].File)
		}
		if len(got) >= 2 && got[1].File != "projA/main.go" {
			t.Errorf("second entry should be projA/main.go, got %s", got[1].File)
		}
		if len(got) >= 3 && got[2].File != "projA/utils.go" {
			t.Errorf("third entry should be projA/utils.go, got %s", got[2].File)
		}
	})

	t.Run("cap per project", func(t *testing.T) {
		t.Parallel()
		got := diversify(sources, 0, 2)
		if len(got) != 4 {
			t.Errorf("diversify(maxPerProject=2) = %d, want 4 (projA capped at 2, projB at 2)", len(got))
		}
	})

	t.Run("no caps", func(t *testing.T) {
		t.Parallel()
		got := diversify(sources, 0, 0)
		if len(got) != len(sources) {
			t.Errorf("diversify(no caps) = %d, want %d", len(got), len(sources))
		}
	})

	t.Run("empty input", func(t *testing.T) {
		t.Parallel()
		if got := diversify(nil, 2, 2); len(got) != 0 {
			t.Error("diversify(nil) should return empty")
		}
	})

	t.Run("both caps active", func(t *testing.T) {
		t.Parallel()
		got := diversify(sources, 1, 3)
		// per-file cap=1: each file at most 1 entry
		// per-project cap=3: each project at most 3 entries
		// projA has 2 files (main.go, utils.go) -> 2 entries
		// projB has 1 file (app.go) -> 1 entry
		// total = 3
		if len(got) != 3 {
			t.Errorf("diversify(maxPerFile=1, maxPerProject=3) = %d, want 3", len(got))
		}
		// Each file should appear at most once
		files := make(map[string]int)
		for _, s := range got {
			files[s.File]++
		}
		for file, count := range files {
			if count > 1 {
				t.Errorf("file %s appears %d times, want ≤1", file, count)
			}
		}
	})
}

func TestExtractProject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"projA/main.go", "projA"},
		{"projA/sub/file.go", "projA"},
		{"projB/app.go", "projB"},
		{"vendor/pkg/lib.go", "vendor"},
		{"foo/bar/baz", "foo"},
		{"noslash", "default"},
		{"", "default"},
		{"/absolute/path", "/absolute"},      // leading slash: first char is '/'
		{"C:/Windows/file.go", "C"}, // colon triggers first, returning "C"
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := extractProject(tc.input)
			if got != tc.want {
				t.Errorf("extractProject(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
