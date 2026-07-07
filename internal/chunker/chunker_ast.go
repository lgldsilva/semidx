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

// chunkCodeASTPrior is the internal implementation for ChunkFilePriorAST:
// chunks content on the given pre-parsed symbol boundaries. When symbols are
// empty it returns nil, signalling the caller to fall back.
func chunkCodeASTPrior(content []byte, maxChars int, syms []analyzer.Symbol) []Chunk {
	if len(syms) == 0 {
		return nil
	}
	return astChunkFromSyms(content, maxChars, syms)
}

// astChunkFromSyms is the shared implementation used by both ChunkFileAST and
// chunkCodeASTPrior: it splits content on symbol boundaries with metadata.
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
		// Non-symbol content before this symbol.
		if zeroBasedStart := sym.StartLine - 1; zeroBasedStart > lastLine {
			nonSym := lines[lastLine:zeroBasedStart]
			if text := strings.TrimSpace(strings.Join(nonSym, "\n")); text != "" {
				chunks = append(chunks, chunkLines(lines, lastLine+1, zeroBasedStart, maxChars)...)
			}
		}

		// Clip symbol end to file bounds.
		symEnd := sym.EndLine
		if symEnd > len(lines) {
			symEnd = len(lines)
		}

		symLines := lines[sym.StartLine-1 : symEnd]
		symContent := strings.Join(symLines, "\n")
		prefix := fmt.Sprintf("[%s] %s\n", sym.Kind, sym.Name)

		if len(prefix)+len(symContent) <= maxChars {
			chunks = append(chunks, Chunk{
				Content:   prefix + symContent,
				StartLine: sym.StartLine,
				EndLine:   symEnd,
			})
		} else {
			chunks = append(chunks, splitSymbolChunks(symContent, prefix, sym.StartLine, maxChars)...)
		}

		lastLine = symEnd
	}

	// Trailing content after the last symbol.
	if lastLine < len(lines) {
		tail := strings.TrimSpace(strings.Join(lines[lastLine:], "\n"))
		if tail != "" {
			chunks = append(chunks, chunkLines(lines, lastLine+1, len(lines), maxChars)...)
		}
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

		spaceLeft := maxChars - prefixLen
		// Take up to spaceLeft bytes; try to cut at a newline past halfway.
		end := spaceLeft
		if end > len(remaining) {
			end = len(remaining)
		}
		piece := remaining[:end]
		if end < len(remaining) {
			if nl := strings.LastIndex(piece, "\n"); nl > spaceLeft/2 {
				piece = piece[:nl]
			}
			// Avoid cutting a multi-byte rune at the end.
			for len(piece) > 0 && !strings.HasPrefix(remaining[len(piece):], "\n") &&
				len(piece) > 0 && piece[len(piece)-1] != '\n' &&
				len(piece) > 0 && (piece[len(piece)-1]&0x80 != 0) {
				piece = piece[:len(piece)-1]
			}
		}

		if piece == "" {
			// Safeguard: if piece is empty after trimming, advance by maxChars bytes.
			piece = remaining
			if len(piece) > maxChars {
				piece = piece[:maxChars]
			}
		}

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
