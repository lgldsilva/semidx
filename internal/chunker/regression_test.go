package chunker

import (
	"strings"
	"testing"
)

// A single line longer than bufio.Scanner's default 64 KiB token limit used to
// stop the scan silently, dropping every line after it.
func TestChunkCodeKeepsContentAfterHugeLine(t *testing.T) {
	src := "x := 1\n\n" + strings.Repeat("y", 70000) + "\n\nz := 2\n"
	chunks := chunkCode([]byte(src), 1000)
	var all strings.Builder
	for _, c := range chunks {
		all.WriteString(c.Content)
	}
	if !strings.Contains(all.String(), "z := 2") {
		t.Fatalf("content after a >64KiB line was dropped; got %d chunks", len(chunks))
	}
	if last := chunks[len(chunks)-1]; last.StartLine != 5 {
		t.Fatalf("last chunk start line = %d, want 5", last.StartLine)
	}
}

// Leading/consecutive blank lines must not produce empty chunks: embedding
// APIs reject empty inputs and the whole batch fails.
func TestChunkCodeNoEmptyChunks(t *testing.T) {
	chunks := chunkCode([]byte("a\n\n\n\nb"), 1000)
	if len(chunks) != 2 {
		t.Fatalf("want 2 chunks, got %d: %+v", len(chunks), chunks)
	}
	if chunks[1].Content != "b" || chunks[1].StartLine != 5 {
		t.Fatalf("second chunk = %+v, want b@5", chunks[1])
	}
}

func TestChunkFileDropsWhitespaceOnlyChunks(t *testing.T) {
	for _, path := range []string{"notes.txt", "main.go", "x.unknownext.md"} {
		for _, c := range ChunkFile(path, []byte("\n\n   \n\t\n"), 100) {
			if strings.TrimSpace(c.Content) == "" {
				t.Fatalf("%s: empty chunk emitted: %+v", path, c)
			}
		}
	}
}
