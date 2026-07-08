// Package chunker — AST-aware chunking using tree-sitter symbol boundaries.
//
// When the analyzer returns symbols for a file (tree-sitter grammar available),
// this file provides chunking on function/class/method boundaries instead of
// the generic blank-line split. Each symbol becomes its own chunk; oversized
// symbols are split internally but never across boundaries.
package chunker

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lgldsilva/semidx/internal/analyzer"
)

// ChunkFileAST splits content on symbol boundaries (functions, classes, methods)
// when the analyzer provides symbols for the file extension. It returns nil when
// no symbols are available, signalling the caller to fall back to blank-line
// based chunking (chunkCode).
//
// Each symbol becomes one or more chunks. Non-symbol content (package decls,
// imports, top-level vars) is collected into leading/trailing/interstitial
// chunks using the blank-line split strategy so nothing is lost.
func ChunkFileAST(path string, content []byte, maxChars int) []Chunk {
	syms := analyzer.Symbols(path, content)
	if len(syms) == 0 {
		return nil
	}
	return astChunkFromSyms(content, maxChars, syms)
}

// astChunkFromSyms splits content on symbol boundaries with metadata.
func astChunkFromSyms(content []byte, maxChars int, syms []analyzer.Symbol) []Chunk {
	// Sort symbols by start line.
	sorted := make([]analyzer.Symbol, len(syms))
	copy(sorted, syms)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].StartLine < sorted[j].StartLine
	})

	lines := strings.Split(string(content), "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}

	var chunks []Chunk
	lastLine := 0 // 0-indexed line processed so far

	for _, sym := range sorted {
		zeroBasedStart := sym.StartLine - 1
		chunks = appendNonSymbolChunks(chunks, lines, lastLine, zeroBasedStart, maxChars)

		symEnd := clipSymbolEnd(sym.EndLine, len(lines))
		chunks = appendSymbolChunks(chunks, lines, sym, symEnd, maxChars)
		lastLine = symEnd
	}

	return appendTrailingChunks(chunks, lines, lastLine, maxChars)
}

// appendNonSymbolChunks emits blank-line-split chunks for content between
// lastLine and zeroBasedStart (both 0-indexed) when that range is non-empty.
func appendNonSymbolChunks(chunks []Chunk, lines []string, lastLine, zeroBasedStart, maxChars int) []Chunk {
	if zeroBasedStart <= lastLine {
		return chunks
	}
	nonSym := lines[lastLine:zeroBasedStart]
	if text := strings.TrimSpace(strings.Join(nonSym, "\n")); text != "" {
		chunks = append(chunks, chunkLines(lines, lastLine+1, zeroBasedStart, maxChars)...)
	}
	return chunks
}

// clipSymbolEnd clamps a symbol's end line to the file's line count.
func clipSymbolEnd(symEnd, lineCount int) int {
	if symEnd > lineCount {
		return lineCount
	}
	return symEnd
}

// appendSymbolChunks emits one or more chunks for a single symbol, splitting
// internally when the symbol content exceeds maxChars.
func appendSymbolChunks(chunks []Chunk, lines []string, sym analyzer.Symbol, symEnd, maxChars int) []Chunk {
	symLines := lines[sym.StartLine-1 : symEnd]
	symContent := strings.Join(symLines, "\n")
	prefix := fmt.Sprintf("[%s] %s\n", sym.Kind, sym.Name)

	if len(prefix)+len(symContent) <= maxChars {
		return append(chunks, Chunk{
			Content:   prefix + symContent,
			StartLine: sym.StartLine,
			EndLine:   symEnd,
		})
	}
	return append(chunks, splitSymbolChunks(symContent, prefix, sym.StartLine, maxChars)...)
}

// appendTrailingChunks emits blank-line-split chunks for content after the last
// symbol when that trailing range is non-empty.
func appendTrailingChunks(chunks []Chunk, lines []string, lastLine, maxChars int) []Chunk {
	if lastLine >= len(lines) {
		return chunks
	}
	tail := strings.TrimSpace(strings.Join(lines[lastLine:], "\n"))
	if tail != "" {
		chunks = append(chunks, chunkLines(lines, lastLine+1, len(lines), maxChars)...)
	}
	return chunks
}

// splitSymbolChunks splits oversized symbol content into multiple chunks, each
// prefixed with the symbol metadata. Pieces never cross the symbol's line range.
func splitSymbolChunks(content, prefix string, startLine int, maxChars int) []Chunk {
	var chunks []Chunk
	remaining := content
	currentLine := startLine
	prefixLen := len(prefix)

	for len(remaining) > 0 {
		// If the prefix alone exceeds maxChars, fall back to un-prefixed rune split.
		if prefixLen >= maxChars {
			return append(chunks, splitOversizedPrefix(remaining, prefix, currentLine, maxChars)...)
		}

		spaceLeft := maxChars - prefixLen
		piece := cutSymbolPiece(remaining, spaceLeft, maxChars)

		pieceLines := strings.Count(piece, "\n") + 1
		chunks = append(chunks, Chunk{
			Content:   prefix + piece,
			StartLine: currentLine,
			EndLine:   currentLine + pieceLines - 1,
		})
		currentLine += pieceLines
		remaining = remaining[len(piece):]
	}

	return chunks
}

// splitOversizedPrefix handles the degenerate case where the metadata prefix
// alone exceeds maxChars: it drops the prefix and splits the combined text by
// runes, returning chunks with no symbol prefix.
func splitOversizedPrefix(remaining, prefix string, currentLine, maxChars int) []Chunk {
	var chunks []Chunk
	for _, piece := range splitRunes(prefix+remaining, maxChars) {
		pieceLines := strings.Count(piece, "\n") + 1
		chunks = append(chunks, Chunk{
			Content:   piece,
			StartLine: currentLine,
			EndLine:   currentLine + pieceLines - 1,
		})
		currentLine += pieceLines
	}
	return chunks
}

// cutSymbolPiece extracts a chunk-sized piece from remaining, trying to cut at
// a newline past the halfway point and trimming any partial UTF-8 rune at the
// boundary. If trimming yields an empty piece, it falls back to maxChars bytes.
func cutSymbolPiece(remaining string, spaceLeft, maxChars int) string {
	end := spaceLeft
	if end > len(remaining) {
		end = len(remaining)
	}
	piece := remaining[:end]
	if end >= len(remaining) {
		return piece
	}
	// Try to cut at a newline past halfway.
	if nl := strings.LastIndex(piece, "\n"); nl > spaceLeft/2 {
		piece = piece[:nl]
	}
	piece = trimRuneBoundary(piece)
	if piece == "" {
		// Safeguard: if piece is empty after trimming, advance by maxChars bytes.
		piece = remaining
		if len(piece) > maxChars {
			piece = piece[:maxChars]
		}
	}
	return piece
}

// trimRuneBoundary removes trailing bytes that are part of a split multi-byte
// UTF-8 rune, stopping at a newline or a clean rune boundary.
func trimRuneBoundary(piece string) string {
	for len(piece) > 0 && piece[len(piece)-1] != '\n' &&
		(piece[len(piece)-1]&0x80 != 0) {
		piece = piece[:len(piece)-1]
	}
	return piece
}

// chunkLines splits a range of lines (1‑based start/end exclusive) into chunks
// using the blank-line strategy. This handles non-symbol content the same way
// the existing chunkCode does, so interstitials are consistent.
func chunkLines(lines []string, startLine, endLine int, maxChars int) []Chunk {
	if startLine < 1 {
		startLine = 1
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}
	if startLine > endLine {
		return nil
	}

	// Take a sub-slice from the 0‑indexed slice.
	sub := lines[startLine-1 : endLine]

	acc := codeAccumulator{maxChars: maxChars}
	lineNum := startLine

	for _, line := range sub {
		switch {
		case strings.TrimSpace(line) == "" && acc.current.Len() > 0:
			acc.flush()
		case len(line) > maxChars:
			acc.appendOversizedLine(line, lineNum)
		default:
			acc.appendLine(line, lineNum)
		}
		lineNum++
	}
	acc.flush()

	if len(acc.chunks) == 0 {
		return nil
	}
	return acc.chunks
}
