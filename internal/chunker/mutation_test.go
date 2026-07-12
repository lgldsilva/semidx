package chunker

import "testing"

// These tests pin the EXACT chunking behavior (boundaries, arithmetic, line
// numbers) rather than loose invariants, so a mutation to any comparison,
// offset or increment in chunkText/chunkCode/lineOf/splitRunes flips an output
// and fails a test. They were written to kill surviving mutants that the
// property-based tests (which only check loose invariants) let live.

func eqChunks(t *testing.T, got []Chunk, want []Chunk) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("chunk count = %d, want %d; got %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("chunk[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestLineOfExact(t *testing.T) {
	const s = "a\nb\nc" // a(0) \n(1) b(2) \n(3) c(4)
	cases := map[int]int{-3: 1, 0: 1, 1: 1, 2: 2, 3: 2, 4: 3, 5: 3, 100: 3}
	for off, want := range cases {
		if got := lineOf(s, off); got != want {
			t.Errorf("lineOf(%q, %d) = %d, want %d", s, off, got, want)
		}
	}
}

func TestSplitRunesExact(t *testing.T) {
	cases := []struct {
		s    string
		maxB int
		want []string
	}{
		{"abcd", 2, []string{"ab", "cd"}}, // even split on ASCII
		{"a€", 2, []string{"a", "€"}},     // never split the 3-byte rune
		{"€", 2, []string{"€"}},           // a single rune wider than the budget is emitted whole
		{"ab", 5, []string{"ab"}},         // shorter than the budget: one piece
	}
	for _, c := range cases {
		got := splitRunes(c.s, c.maxB)
		if len(got) != len(c.want) {
			t.Fatalf("splitRunes(%q,%d) = %q, want %q", c.s, c.maxB, got, c.want)
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("splitRunes(%q,%d)[%d] = %q, want %q", c.s, c.maxB, i, got[i], c.want[i])
			}
		}
	}
}

func TestChunkTextExact(t *testing.T) {
	// Newline PAST the halfway point (index 6 > 10/2) → break at the newline.
	eqChunks(t, chunkText([]byte("123456\n89012345"), 10), []Chunk{
		{Content: "123456", StartLine: 1, EndLine: 1},
		{Content: "89012345", StartLine: 1, EndLine: 2},
	})
	// Newline EXACTLY at the halfway point (index 5, not > 5) → do NOT break;
	// snap on a rune boundary instead, so the newline stays inside chunk 0.
	eqChunks(t, chunkText([]byte("12345\nXXXXXXXXX"), 10), []Chunk{
		{Content: "12345\nXXXX", StartLine: 1, EndLine: 2},
		{Content: "XXXXXX", StartLine: 2, EndLine: 2},
	})
	// Whole text within budget → a single chunk spanning both lines.
	eqChunks(t, chunkText([]byte("hello\nworld"), 100), []Chunk{
		{Content: "hello\nworld", StartLine: 1, EndLine: 2},
	})
}

func TestChunkCodeExact(t *testing.T) {
	// Blank line flushes the accumulated chunk; the blank line's number is skipped.
	eqChunks(t, chunkCode([]byte("a\n\nb"), 100), []Chunk{
		{Content: "a", StartLine: 1, EndLine: 1},
		{Content: "b", StartLine: 3, EndLine: 3},
	})
	// A single line longer than the budget is hard-split on rune boundaries,
	// every piece mapping to the same source line.
	eqChunks(t, chunkCode([]byte("abcdefghij"), 5), []Chunk{
		{Content: "abcde", StartLine: 1, EndLine: 1},
		{Content: "fghij", StartLine: 1, EndLine: 1},
	})
	// Budget crossing (lines carry their trailing newline, so 3 lines at budget 8
	// flush one per chunk) — pins the current.Len()+len(line)+1 > maxChars math.
	eqChunks(t, chunkCode([]byte("aaa\nbbb\nccc"), 8), []Chunk{
		{Content: "aaa", StartLine: 1, EndLine: 1},
		{Content: "bbb", StartLine: 2, EndLine: 2},
		{Content: "ccc", StartLine: 3, EndLine: 3},
	})
}
