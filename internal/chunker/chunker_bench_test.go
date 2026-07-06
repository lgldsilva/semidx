package chunker

import (
	"strings"
	"testing"
)

// BenchmarkChunkFile_1KB benchmarks ChunkFile with roughly 1 KB of Go source
// content, simulating a small single-function file.
func BenchmarkChunkFile_1KB(b *testing.B) {
	b.ReportAllocs()
	text := []byte(strings.Repeat("package main\n\nfunc handler(w http.ResponseWriter, r *http.Request) {\n\tfmt.Println(\"hello\")\n\treturn\n}\n", 20))
	b.ResetTimer()
	for b.Loop() {
		ChunkFile("handler.go", text, 80)
	}
}

// BenchmarkChunkFile_10KB benchmarks ChunkFile with roughly 10 KB of Go source
// content, simulating a small module.
func BenchmarkChunkFile_10KB(b *testing.B) {
	b.ReportAllocs()
	text := []byte(strings.Repeat("package main\n\nfunc handler(w http.ResponseWriter, r *http.Request) {\n\tfmt.Println(\"hello\")\n\treturn\n}\n\nfunc process(data []byte) (string, error) {\n\tresult := strings.TrimSpace(string(data))\n\treturn result, nil\n}\n", 200))
	b.ResetTimer()
	for b.Loop() {
		ChunkFile("server.go", text, 80)
	}
}

// BenchmarkChunkFile_100KB benchmarks ChunkFile with roughly 100 KB of Go
// source content, simulating a larger file across many blank-line-separated
// blocks.
func BenchmarkChunkFile_100KB(b *testing.B) {
	b.ReportAllocs()
	text := []byte(strings.Repeat("package main\n\nfunc handler(w http.ResponseWriter, r *http.Request) {\n\tfmt.Println(\"hello\")\n\treturn\n}\n\nfunc process(data []byte) (string, error) {\n\tresult := strings.TrimSpace(string(data))\n\treturn result, nil\n}\n\nfunc validate(input string) bool {\n\treturn len(input) > 0\n}\n", 2000))
	b.ResetTimer()
	for b.Loop() {
		ChunkFile("server.go", text, 80)
	}
}

// BenchmarkChunkFile_Prose benchmarks ChunkFile with prose-style content
// (Markdown), exercising the prose (text) chunking path.
func BenchmarkChunkFile_Prose(b *testing.B) {
	b.ReportAllocs()
	text := []byte(strings.Repeat("This is a paragraph explaining a concept. It contains multiple sentences and will be split into prose chunks with overlap.\n\nAnother paragraph introducing a different idea. This one is also fairly long and should be chunked appropriately.\n\n", 100))
	b.ResetTimer()
	for b.Loop() {
		ChunkFile("README.md", text, 200)
	}
}

// BenchmarkChunkFile_LargeMaxChars benchmarks ChunkFile with a generous
// maxChars limit, so almost everything fits into one chunk — isolating the
// line-scan overhead rather than the chunk-boundary logic.
func BenchmarkChunkFile_LargeMaxChars(b *testing.B) {
	b.ReportAllocs()
	text := []byte(strings.Repeat("package main\n\nfunc handler(w http.ResponseWriter, r *http.Request) {\n\tfmt.Println(\"hello\")\n\treturn\n}\n", 500))
	b.ResetTimer()
	for b.Loop() {
		ChunkFile("handler.go", text, 999999)
	}
}
