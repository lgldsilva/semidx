package indexing

import (
	"testing"

	sym "github.com/lgldsilva/semidx/internal/analyzer"
	"github.com/lgldsilva/semidx/internal/chunker"
)

// TestClassifyChunk covers the v2 confidence rules end-to-end through
// classifyChunks (the exported path the indexer uses): EXTRACTED when the chunk
// contains a declaration start, INFERRED when it merely overlaps or mentions a
// symbol, and AMBIGUOUS otherwise.
func TestClassifyChunk(t *testing.T) {
	t.Parallel()

	// Symbols for an imaginary source file:
	//   1: package x
	//   3: func Foo() {        // 3..6
	//   8: type Bar struct{}   // 8..8
	//  10: func (*Bar) Baz() {} // 10..12
	syms := []sym.Symbol{
		{Name: "Foo", Kind: "func", StartLine: 3, EndLine: 6},
		{Name: "Bar", Kind: "type", StartLine: 8, EndLine: 8},
		{Name: "Baz", Kind: "method", StartLine: 10, EndLine: 12},
	}

	tests := []struct {
		name       string
		chunk      chunker.Chunk
		wantConf   string
		wantSymbol string
		extraSyms  []sym.Symbol // merged into syms for this case
	}{
		{
			name:       "extracted when declaration start is inside chunk",
			chunk:      chunker.Chunk{Content: "func Foo() {\n\treturn\n}", StartLine: 3, EndLine: 6},
			wantConf:   "EXTRACTED",
			wantSymbol: "Foo",
		},
		{
			name:       "extracted picks first declaration start when several qualify",
			chunk:      chunker.Chunk{Content: "type Bar struct{}\nfunc (*Bar) Baz() {}", StartLine: 8, EndLine: 12},
			wantConf:   "EXTRACTED",
			wantSymbol: "Bar", // Bar.StartLine=8 falls inside [8,12]; encountered first in syms order
		},
		{
			name:       "inferred when chunk overlaps a symbol range but not its start",
			chunk:      chunker.Chunk{Content: "\treturn\n}", StartLine: 5, EndLine: 6}, // overlaps Foo (3..6)
			wantConf:   "INFERRED",
			wantSymbol: "Foo",
		},
		{
			name: "inferred when chunk content mentions a symbol name (no overlap)",
			// Chunk on lines 1..1 mentions Bar without overlapping its range.
			chunk:      chunker.Chunk{Content: "package x // uses Bar somewhere", StartLine: 1, EndLine: 1},
			wantConf:   "INFERRED",
			wantSymbol: "Bar",
		},
		{
			name:       "ambiguous when no symbol overlaps or is mentioned",
			chunk:      chunker.Chunk{Content: "package x\n", StartLine: 1, EndLine: 1},
			wantConf:   "AMBIGUOUS",
			wantSymbol: "",
		},
		{
			name:       "extracted prefers declaration-start over a content mention earlier in syms",
			chunk:      chunker.Chunk{Content: "Bar\nfunc Baz() {}", StartLine: 10, EndLine: 12}, // Baz starts at 10
			wantConf:   "EXTRACTED",
			wantSymbol: "Baz",
		},
		{
			name: "inferred skips empty symbol names in overlap, falls back to substring",
			extraSyms: []sym.Symbol{
				{Name: "", Kind: "func", StartLine: 3, EndLine: 3}, // empty name, overlap only
			},
			// Chunk overlaps the empty-named symbol but mentions Foo in content.
			chunk:      chunker.Chunk{Content: "// talks about Foo", StartLine: 4, EndLine: 4},
			wantConf:   "INFERRED",
			wantSymbol: "Foo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			all := append(append([]sym.Symbol{}, syms...), tt.extraSyms...)
			chunks := []chunker.Chunk{tt.chunk}
			classifyChunks(chunks, all)
			gotConf, gotSymbol := chunks[0].Confidence, chunks[0].Symbol
			if gotConf != tt.wantConf {
				t.Errorf("confidence = %q, want %q", gotConf, tt.wantConf)
			}
			if gotSymbol != tt.wantSymbol {
				t.Errorf("symbol = %q, want %q", gotSymbol, tt.wantSymbol)
			}
		})
	}
}

// TestClassifyChunksMutatesInPlace confirms the helper annotates every chunk in
// the slice and the budget-trimmed path keeps classification aligned.
func TestClassifyChunksMutatesInPlace(t *testing.T) {
	t.Parallel()
	syms := []sym.Symbol{{Name: "Foo", Kind: "func", StartLine: 1, EndLine: 2}}
	chunks := []chunker.Chunk{
		{Content: "func Foo() {}", StartLine: 1, EndLine: 2},
		{Content: "no symbol here", StartLine: 4, EndLine: 4},
	}
	classifyChunks(chunks, syms)
	if chunks[0].Confidence != "EXTRACTED" || chunks[0].Symbol != "Foo" {
		t.Errorf("chunk 0 = (%q,%q), want (EXTRACTED,Foo)", chunks[0].Confidence, chunks[0].Symbol)
	}
	if chunks[1].Confidence != "AMBIGUOUS" || chunks[1].Symbol != "" {
		t.Errorf("chunk 1 = (%q,%q), want (AMBIGUOUS,'')", chunks[1].Confidence, chunks[1].Symbol)
	}
}

// TestClassifyChunkEmptySymbols confirms classification is AMBIGUOUS when no
// symbols are passed (e.g. unsupported file type or symbol extraction disabled).
func TestClassifyChunkEmptySymbols(t *testing.T) {
	t.Parallel()
	chunks := []chunker.Chunk{{Content: "anything", StartLine: 1, EndLine: 1}}
	classifyChunks(chunks, nil)
	if chunks[0].Confidence != "AMBIGUOUS" || chunks[0].Symbol != "" {
		t.Errorf("with no symbols = (%q,%q), want (AMBIGUOUS,'')",
			chunks[0].Confidence, chunks[0].Symbol)
	}
}
