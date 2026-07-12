package chunker

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func TestShouldIndex(t *testing.T) {
	cases := map[string]bool{
		"main.go":                true,
		"docs/readme.md":         true,
		"config.yaml":            true,
		"pkg/util.py":            true,
		"image.png":              false,
		"bin/app":                false, // no known extension
		"node_modules/react.js":  false, // ignored dir
		"src/.git/config":        false, // ignored dir mid-path
		"vendor/lib/thing.go":    false, // ignored dir
		"archive.tar.gz":         false, // ignored ext
		"a/b/c/deep.ts":          true,
		"nested/build/output.js": false, // ignored dir mid-path
		"weird.unknownextension": false,
	}
	for path, want := range cases {
		if got := ShouldIndex(path); got != want {
			t.Errorf("ShouldIndex(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestIsIgnoredDir(t *testing.T) {
	for _, d := range []string{".git", "node_modules", "vendor", "__pycache__"} {
		if !IsIgnoredDir(d) {
			t.Errorf("IsIgnoredDir(%q) = false, want true", d)
		}
	}
	for _, d := range []string{"src", "internal", "cmd"} {
		if IsIgnoredDir(d) {
			t.Errorf("IsIgnoredDir(%q) = true, want false", d)
		}
	}
}

func TestChunkFileEmpty(t *testing.T) {
	if got := ChunkFile("x.go", nil, 100); len(got) != 0 {
		t.Errorf("ChunkFile(empty) = %d chunks, want 0", len(got))
	}
}

func TestChunkTextSingleWhenSmall(t *testing.T) {
	chunks := ChunkFile("notes.md", []byte("short text"), 100)
	if len(chunks) != 1 || chunks[0].Content != "short text" {
		t.Errorf("small text: got %#v, want single chunk 'short text'", chunks)
	}
}

// Property: no chunk ever exceeds maxChars and every chunk carries a valid
// 1-based line range. These are the invariants the indexer and search rely on.
func TestChunkNeverExceedsMax(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		content := []byte(rapid.String().Draw(t, "content"))
		maxChars := rapid.IntRange(20, 500).Draw(t, "maxChars")
		ext := rapid.SampledFrom([]string{".go", ".md", ".txt", ".py"}).Draw(t, "ext")

		chunks := ChunkFile("f"+ext, content, maxChars)
		prevStart := 0
		for i, c := range chunks {
			if len(c.Content) > maxChars {
				t.Fatalf("chunk %d len %d exceeds maxChars %d (ext %s)", i, len(c.Content), maxChars, ext)
			}
			if c.StartLine < 1 || c.EndLine < c.StartLine {
				t.Fatalf("chunk %d has invalid line range [%d,%d]", i, c.StartLine, c.EndLine)
			}
			if c.StartLine < prevStart {
				t.Fatalf("chunk %d StartLine %d < previous %d (not non-decreasing)", i, c.StartLine, prevStart)
			}
			prevStart = c.StartLine
		}
	})
}

func TestChunkLineNumbers(t *testing.T) {
	code := "package main\n\nfunc A() {\n\treturn\n}\n\nfunc B() {}\n"
	chunks := ChunkFile("x.go", []byte(code), 4000)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d: %#v", len(chunks), chunks)
	}
	want := []struct{ start, end int }{{1, 1}, {3, 5}, {7, 7}}
	for i, w := range want {
		if chunks[i].StartLine != w.start || chunks[i].EndLine != w.end {
			t.Errorf("chunk %d line range [%d,%d], want [%d,%d]", i, chunks[i].StartLine, chunks[i].EndLine, w.start, w.end)
		}
	}
}

// isSubsequence reports whether sub appears within s in order (gaps allowed).
func isSubsequence(sub, s []rune) bool {
	i := 0
	for _, r := range s {
		if i < len(sub) && sub[i] == r {
			i++
		}
	}
	return i == len(sub)
}

// Property: for prose, chunking never loses content. Chunks are ordered slices
// with overlap, so the input's non-space runes must appear as a subsequence of
// the concatenated chunks (overlap may duplicate, but nothing is dropped or
// corrupted — this also proves multi-byte runes are never split).
func TestChunkTextCoversContent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		content := rapid.String().Draw(t, "content")
		maxChars := rapid.IntRange(20, 300).Draw(t, "maxChars")

		chunks := ChunkFile("f.md", []byte(content), maxChars)
		var joined strings.Builder
		for _, c := range chunks {
			joined.WriteString(c.Content)
		}
		want := []rune(strings.Join(strings.Fields(content), ""))
		got := []rune(strings.Join(strings.Fields(joined.String()), ""))
		if !isSubsequence(want, got) {
			t.Fatalf("content lost: %q is not a subsequence of chunk output %q", string(want), string(got))
		}
	})
}
