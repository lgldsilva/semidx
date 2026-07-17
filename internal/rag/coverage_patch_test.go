// coverage-patch: 2026-07-17
package rag

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFilterSensitiveSources(t *testing.T) {
	t.Parallel()
	results := []SearchResult{
		{FilePath: "src/main.go", Content: "ok", Score: 0.9, StartLine: 1, EndLine: 2},
		{FilePath: "config/secrets.env", Content: "SECRET=1", Score: 0.8, StartLine: 1, EndLine: 1},
		{FilePath: "keys/api_key.pem", Content: "-----", Score: 0.7, StartLine: 1, EndLine: 1},
		{FilePath: "pkg/util.go", Content: "util", Score: 0.6, StartLine: 3, EndLine: 5},
	}
	got := filterSensitiveSources(results)
	if len(got) != 2 {
		t.Fatalf("filterSensitiveSources kept %d, want 2 (non-sensitive only): %+v", len(got), got)
	}
	if got[0].File != "src/main.go" || got[1].File != "pkg/util.go" {
		t.Errorf("unexpected files: %+v", got)
	}
	if got[0].StartLine != 1 || got[0].Score != 0.9 {
		t.Errorf("fields not copied: %+v", got[0])
	}
}

func TestAssembleContext_emptyAndBudget(t *testing.T) {
	t.Parallel()

	if got := assembleContext(nil, 100); got != "" {
		t.Errorf("nil sources: %q", got)
	}
	if got := assembleContext([]Source{{File: "a.go", Content: "x"}}, 0); got != "" {
		t.Errorf("zero budget: %q", got)
	}

	sources := []Source{
		{File: "a.go", StartLine: 1, EndLine: 2, Content: "func A() {}"},
		{File: "b.go", StartLine: 3, EndLine: 4, Content: "func B() {}"},
	}
	// Large budget: both blocks fully included.
	full := assembleContext(sources, 10_000)
	if !strings.Contains(full, "a.go") || !strings.Contains(full, "b.go") {
		t.Errorf("full context missing sources: %q", full)
	}
	if !strings.Contains(full, "---") {
		t.Errorf("expected block markers: %q", full)
	}
}

func TestAssembleContext_truncationAndMinSnippet(t *testing.T) {
	t.Parallel()

	// First source is huge; budget forces truncation at rune boundary.
	long := strings.Repeat("字", 200) // multi-byte runes
	sources := []Source{
		{File: "big.go", StartLine: 1, EndLine: 10, Content: long},
		{File: "skip.go", StartLine: 1, EndLine: 1, Content: "should not appear after truncate"},
	}
	// budgetTokens * 4 chars — pick a budget that fits header + partial content
	// but not the full block.
	out := assembleContext(sources, 40) // 160 chars
	if out == "" {
		t.Fatal("expected partial context, got empty")
	}
	if strings.Contains(out, "should not appear") {
		t.Error("second source must not be added after truncation")
	}
	if !utf8.ValidString(out) {
		t.Error("truncated context must stay valid UTF-8")
	}
	// Closing marker retained.
	if !strings.Contains(out, "---") {
		t.Errorf("expected closing marker: %q", out)
	}

	// Budget so small that remaining < 60 after first full block would not fit:
	// empty first source with tiny content that fits, then break on remaining.
	tiny := []Source{
		{File: "t.go", StartLine: 1, EndLine: 1, Content: "x"},
		{File: "u.go", StartLine: 1, EndLine: 1, Content: strings.Repeat("y", 500)},
	}
	// First block is short; after writing it remaining may be < 60 → break.
	out2 := assembleContext(tiny, 20) // 80 chars — first block ~60-70 chars
	if strings.Contains(out2, "u.go") {
		// Depending on exact block size, u.go may be truncated instead of skipped.
		// Either way we must not panic and stay valid UTF-8.
		if !utf8.ValidString(out2) {
			t.Error("invalid utf8")
		}
	}
}

func TestAssembleContext_budgetExhaustedBreak(t *testing.T) {
	t.Parallel()
	// Fill budget exactly with first source so b.Len() >= budgetChars on next iter.
	content := strings.Repeat("a", 100)
	s := Source{File: "f.go", StartLine: 1, EndLine: 1, Content: content}
	blockLen := len(formatSourceBlock(s))
	// budgetTokens such that budgetChars == blockLen (fits first fully).
	budgetTokens := blockLen / 4
	if budgetTokens*4 < blockLen {
		budgetTokens++
	}
	// Use exact: if first fits and second would need remaining < 60 or zero.
	out := assembleContext([]Source{s, {File: "g.go", Content: "more", StartLine: 1, EndLine: 1}}, budgetTokens)
	if !strings.Contains(out, "f.go") {
		t.Errorf("first source missing: %q", out)
	}
}
