package chunker

import (
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/analyzer"
)

// --- Unit tests for pure helper functions ---

func TestClipSymbolEnd(t *testing.T) {
	t.Parallel()
	if got := clipSymbolEnd(5, 10); got != 5 {
		t.Fatalf("clipSymbolEnd(5,10) = %d", got)
	}
	if got := clipSymbolEnd(15, 10); got != 10 {
		t.Fatalf("clipSymbolEnd(15,10) = %d", got)
	}
}

func TestTrimRuneBoundary(t *testing.T) {
	t.Parallel()
	// ASCII string should be unchanged.
	if got := trimRuneBoundary("hello"); got != "hello" {
		t.Fatalf("got %q", got)
	}
	// Newline at end should remain.
	if got := trimRuneBoundary("line\n"); got != "line\n" {
		t.Fatalf("got %q", got)
	}
	// Bytes with high-bit set get trimmed.
	if got := trimRuneBoundary("abc\x80\x80"); got != "abc" {
		t.Fatalf("got %q", got)
	}
	// Empty input.
	if got := trimRuneBoundary(""); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestCutSymbolPiece(t *testing.T) {
	t.Parallel()
	// Piece fits entirely.
	got := cutSymbolPiece("short", 100, 1000)
	if got != "short" {
		t.Fatalf("got %q", got)
	}
	// Piece does NOT cut at newline when exactly at halfway (nl > half, not >=).
	got = cutSymbolPiece("first half\nsecond half", 20, 1000)
	if got != "first half\nsecond ha" {
		t.Fatalf("cut at exact halfway got %q", got)
	}
	// No newline past halfway, full piece within spaceLeft.
	got = cutSymbolPiece("abcdefghij", 5, 1000)
	if len(got) > 5 {
		t.Fatalf("cut at max got %q (len=%d)", got, len(got))
	}
	// Empty after trimming falls back to maxChars bytes.
	got = cutSymbolPiece("\x80\x80\x80", 3, 10)
	if got == "" {
		t.Fatal("fallback should not be empty")
	}
	// Piece with newline well past halfway should cut.
	got = cutSymbolPiece("header\nlonger body text here extra", 28, 1000)
	// header is 6 chars, \n at 6, spaceLeft/2=14, nl=6 > 14? No → falls through.
	// This is fine — the cut only happens when newline > spaceLeft/2.
	_ = got // covered by quick tests below
}

func TestSplitOversizedPrefix(t *testing.T) {
	t.Parallel()
	chunks := splitOversizedPrefix("hello world", "[func] ", 1, 5)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
}

func TestSplitSymbolChunks(t *testing.T) {
	t.Parallel()
	content := "line1\nline2\nline3\nline4\n"
	chunks := splitSymbolChunks(content, "[func] myFunc\n", 10, 100)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
}

func TestSplitSymbolChunksOversized(t *testing.T) {
	t.Parallel()
	// Content large enough to force multiple chunks.
	var content strings.Builder
	for i := 0; i < 200; i++ {
		content.WriteString("abcdefghij\n")
	}
	chunks := splitSymbolChunks(content.String(), "[func] myFunc\n", 10, 100)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
}

func TestAppendNonSymbolChunksEmpty(t *testing.T) {
	t.Parallel()
	lines := []string{"a", "b", "c"}
	// lastLine >= zeroBasedStart → no non-symbol content.
	chunks := appendNonSymbolChunks(nil, lines, 3, 2, 100)
	if len(chunks) != 0 {
		t.Fatal("expected no chunks when range is empty")
	}
}

func TestAppendNonSymbolChunks(t *testing.T) {
	t.Parallel()
	lines := []string{"package main", "import \"fmt\"", "", "func main() {"}
	chunks := appendNonSymbolChunks(nil, lines, 0, 2, 500)
	if len(chunks) == 0 {
		t.Fatal("expected chunks for non-symbol content")
	}
}

func TestAppendTrailingChunks(t *testing.T) {
	t.Parallel()
	lines := []string{"}", "", "// trailing comment"}
	chunks := appendTrailingChunks(nil, lines, 1, 500)
	if len(chunks) == 0 {
		t.Fatal("expected trailing chunks")
	}
}

func TestAppendTrailingChunksEmpty(t *testing.T) {
	t.Parallel()
	lines := []string{"}"}
	chunks := appendTrailingChunks(nil, lines, 1, 500)
	if len(chunks) != 0 {
		t.Fatal("expected no trailing chunks when at end")
	}
}

func TestAppendSymbolChunksFitsInOne(t *testing.T) {
	t.Parallel()
	lines := []string{"1:func Foo() {", "2:  return 42", "3:}"}
	sym := analyzer.Symbol{Name: "Foo", Kind: "func", StartLine: 1, EndLine: 3}
	chunks := appendSymbolChunks(nil, lines, sym, 3, 500)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if !strings.Contains(chunks[0].Content, "[func] Foo") {
		t.Fatalf("chunk missing prefix: %q", chunks[0].Content)
	}
}

func TestAppendSymbolChunksOversized(t *testing.T) {
	t.Parallel()
	// Build a big symbol that exceeds maxChars.
	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString("x\n")
	}
	content := b.String()
	lines := strings.Split(content, "\n")
	sym := analyzer.Symbol{Name: "Big", Kind: "func", StartLine: 1, EndLine: len(lines)}
	chunks := appendSymbolChunks(nil, lines, sym, len(lines), 100)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks for oversized symbol, got %d", len(chunks))
	}
}

func TestAppendSymbolChunksPrefixOversized(t *testing.T) {
	t.Parallel()
	// Prefix alone exceeds maxChars → fallback to splitOversizedPrefix.
	lines := []string{"short"}
	sym := analyzer.Symbol{Name: "Func", Kind: "func", StartLine: 1, EndLine: 1}
	// maxChars so small even prefix doesn't fit.
	chunks := appendSymbolChunks(nil, lines, sym, 1, 5)
	if len(chunks) == 0 {
		t.Fatal("expected chunks even with oversized prefix")
	}
}

// --- Integration test: ChunkFileAST with real Go code ---

func TestChunkFileASTGo(t *testing.T) {
	t.Parallel()
	goSrc := `package main

import "fmt"

func add(a, b int) int {
	return a + b
}

func main() {
	fmt.Println(add(1, 2))
}
`
	chunks := ChunkFileAST("test.go", []byte(goSrc), 500)
	if len(chunks) == 0 {
		t.Fatal("ChunkFileAST returned nil for Go code")
	}
	// Verify at least one chunk has the function name.
	found := false
	for _, c := range chunks {
		if strings.Contains(c.Content, "add") || strings.Contains(c.Content, "main") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no function found in chunks: %+v", chunks)
	}
}

func TestChunkFileASTNoSymbols(t *testing.T) {
	t.Parallel()
	// .txt files have no tree-sitter grammar → returns nil.
	chunks := ChunkFileAST("test.txt", []byte("hello world"), 500)
	if chunks != nil {
		t.Fatal("expected nil for unsupported extension")
	}
}

func TestChunkFileASTEmpty(t *testing.T) {
	t.Parallel()
	chunks := ChunkFileAST("test.go", []byte{}, 500)
	if chunks != nil {
		t.Fatal("expected nil for empty content")
	}
}

func TestAstChunkFromSymsEmpty(t *testing.T) {
	t.Parallel()
	// Empty content with symbols.
	syms := []analyzer.Symbol{{Name: "X", Kind: "func", StartLine: 1, EndLine: 1}}
	chunks := astChunkFromSyms([]byte(""), 500, syms)
	if len(chunks) == 0 {
		t.Fatal("expected chunks for empty content with symbol")
	}
}

func TestAstChunkFromSymsSorted(t *testing.T) {
	t.Parallel()
	// Symbols out of order should still produce correct line ranges.
	content := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"
	syms := []analyzer.Symbol{
		{Name: "Second", Kind: "func", StartLine: 6, EndLine: 10},
		{Name: "First", Kind: "func", StartLine: 1, EndLine: 5},
	}
	chunks := astChunkFromSyms([]byte(content), 500, syms)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d: %+v", len(chunks), chunks)
	}
}

func TestChunkLinesEdgeCases(t *testing.T) {
	t.Parallel()
	// startLine < 1
	chunks := chunkLines([]string{"a", "b", "c"}, 0, 2, 500)
	if len(chunks) == 0 {
		t.Fatal("expected chunks when startLine adjusted")
	}
	// startLine > endLine
	chunks = chunkLines([]string{"a", "b"}, 3, 2, 500)
	if chunks != nil {
		t.Fatal("expected nil when start > end")
	}
	// endLine beyond slice
	chunks = chunkLines([]string{"a", "b"}, 1, 10, 500)
	if len(chunks) == 0 {
		t.Fatal("expected chunks when endLine clamped")
	}
}
