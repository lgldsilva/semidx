package chunker

import (
	"strings"
	"testing"
)

func BenchmarkChunker_Small(b *testing.B) {
	text := []byte(strings.Repeat("package main\n\nfunc main() {\n\tprintln(\"hello world\")\n}\n", 50))
	b.ResetTimer()
	for b.Loop() {
		ChunkFile("main.go", text, 80)
	}
}

func BenchmarkChunker_Large(b *testing.B) {
	text := []byte(strings.Repeat("package main\n\nfunc main() {\n\tfmt.Println(\"hello world\")\n}\n\nfunc handler(w http.ResponseWriter, r *http.Request) {\n\t// handle request\n}\n", 500))
	b.ResetTimer()
	for b.Loop() {
		ChunkFile("server.go", text, 80)
	}
}
