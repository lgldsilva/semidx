package search

import (
	"io"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func BenchmarkHumanFormat(b *testing.B) {
	resp := &Response{
		Model:   "bge-m3",
		Results: make([]store.SearchResult, 100),
	}
	for i := range resp.Results {
		resp.Results[i] = store.SearchResult{
			FilePath:  "internal/server/server.go",
			StartLine: i + 1,
			EndLine:   i + 3,
			Score:     0.75 + float64(i)*0.001,
			Content:   strings.Repeat("x", 200),
		}
	}
	f := HumanFormatter{Preview: 200}
	b.ResetTimer()
	for b.Loop() {
		_ = f.Format(io.Discard, resp)
	}
}
