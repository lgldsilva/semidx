// Package chunker decides which files to index and splits their contents into
// bounded chunks suitable for embedding. It has no dependencies on the database
// or embedding providers, so it is cheap to unit-test in isolation.
package chunker

import (
	"bufio"
	"bytes"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/lgldsilva/semidx/internal/analyzer"
)

// Chunk is one indexable slice of a file's content, with the 1-based line range
// it spans in the source file (so search results can point at a line without
// re-reading the file).
type Chunk struct {
	Content   string
	StartLine int
	EndLine   int
}

var (
	codeExts = map[string]bool{
		".go": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true,
		".java": true, ".py": true, ".rs": true, ".c": true, ".cpp": true,
		".h": true, ".hpp": true, ".cs": true, ".rb": true, ".php": true,
		".swift": true, ".kt": true, ".scala": true, ".sh": true, ".bash": true,
		".yaml": true, ".yml": true, ".json": true, ".toml": true, ".mod": true,
		".sum": true, ".dockerfile": true, ".sql": true,
	}

	textExts = map[string]bool{
		".md": true, ".txt": true, ".adoc": true, ".rst": true,
	}

	ignoredDirs = map[string]bool{
		".git": true, "node_modules": true, "vendor": true, "dist": true,
		"build": true, ".next": true, "target": true, "bin": true, "obj": true,
		"__pycache__": true, ".venv": true, "venv": true, ".idea": true,
		".vscode": true, "coverage": true, ".turbo": true,
	}

	ignoredExts = map[string]bool{
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true,
		".ico": true, ".woff": true, ".woff2": true, ".ttf": true, ".eot": true,
		".pdf": true, ".zip": true, ".tar": true, ".gz": true, ".rar": true,
		".7z": true, ".exe": true, ".dll": true, ".so": true, ".dylib": true,
		".mp3": true, ".mp4": true, ".avi": true, ".mov": true, ".webm": true,
		".mkv": true, ".wav": true, ".flac": true, ".ogg": true,
	}
)

// IsIgnoredDir reports whether a directory of the given base name should be
// skipped entirely during a walk (heavy build/vendor/VCS dirs).
func IsIgnoredDir(name string) bool {
	return ignoredDirs[name]
}

// ShouldIndex reports whether the file at the given (relative) path is a source
// or text file worth indexing, skipping ignored directories and binary/asset
// extensions.
func ShouldIndex(path string) bool {
	parts := strings.Split(path, string(filepath.Separator))
	for _, part := range parts {
		if ignoredDirs[part] {
			return false
		}
	}

	ext := strings.ToLower(filepath.Ext(path))
	if ignoredExts[ext] {
		return false
	}

	return codeExts[ext] || textExts[ext]
}

// ChunkFile splits a file's content into chunks no larger than maxChars,
// choosing a code- or prose-oriented strategy from the file extension.
//
// For languages with a tree-sitter grammar, it uses AST-aware chunking:
// chunks are aligned to function/class/method boundaries with symbol metadata
// prefixes. Falls back to blank-line-based chunking when tree-sitter is not
// available or parsing fails.
func ChunkFile(path string, content []byte, maxChars int) []Chunk {
	ext := strings.ToLower(filepath.Ext(path))
	if codeExts[ext] {
		// Try AST-aware chunking first (tree-sitter symbol boundaries).
		if ast := ChunkFileAST(path, content, maxChars); ast != nil {
			return ast
		}
		return chunkCode(content, maxChars)
	}
	return chunkText(content, maxChars)
}

// ChunkFilePriorAST is like ChunkFile but allows callers that already have
// parsed symbols to pass them directly, avoiding a redundant parse. When syms
// is nil or empty it falls back to the default strategy.
func ChunkFilePriorAST(path string, content []byte, maxChars int, syms []analyzer.Symbol) []Chunk {
	if len(syms) == 0 {
		return ChunkFile(path, content, maxChars)
	}
	ext := strings.ToLower(filepath.Ext(path))
	if codeExts[ext] {
		return chunkCodeASTPrior(content, maxChars, syms)
	}
	return chunkText(content, maxChars)
}

func chunkCode(content []byte, maxChars int) []Chunk {
	// Split by blank lines, then merge runs of lines into chunks up to maxChars,
	// tracking the 1-based source line range of each chunk.
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Split(scanLines)

	acc := codeAccumulator{maxChars: maxChars}
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		switch {
		case strings.TrimSpace(line) == "" && acc.current.Len() > 0:
			// Blank line ends the current run.
			acc.flush()
		case len(line) > maxChars:
			acc.appendOversizedLine(line, lineNum)
		default:
			acc.appendLine(line, lineNum)
		}
	}

	acc.flush()

	// Files without blank lines produce no chunks above; fall back to prose split.
	if len(acc.chunks) == 0 && len(content) > 0 {
		return chunkText(content, maxChars)
	}

	return acc.chunks
}

// codeAccumulator merges code lines into bounded chunks while tracking the
// 1-based source line range of the chunk currently being built.
type codeAccumulator struct {
	chunks   []Chunk
	current  strings.Builder
	curStart int
	curEnd   int
	maxChars int
}

// flush emits the pending chunk (if any) and resets the builder.
func (a *codeAccumulator) flush() {
	if a.current.Len() == 0 {
		return
	}
	a.chunks = append(a.chunks, Chunk{Content: strings.TrimSpace(a.current.String()), StartLine: a.curStart, EndLine: a.curEnd})
	a.current.Reset()
}

// appendLine adds a within-budget line, flushing first if it would overflow the
// current chunk.
func (a *codeAccumulator) appendLine(line string, lineNum int) {
	if a.current.Len()+len(line)+1 > a.maxChars && a.current.Len() > 0 {
		a.flush()
	}
	if a.current.Len() == 0 {
		a.curStart = lineNum
	}
	a.curEnd = lineNum
	a.current.WriteString(line)
	a.current.WriteString("\n")
}

// appendOversizedLine handles a single line longer than the budget: it flushes
// what we have, then hard-splits the line on rune boundaries (all pieces map to
// that one source line).
func (a *codeAccumulator) appendOversizedLine(line string, lineNum int) {
	a.flush()
	for _, piece := range splitRunes(line, a.maxChars) {
		if p := strings.TrimSpace(piece); p != "" {
			a.chunks = append(a.chunks, Chunk{Content: p, StartLine: lineNum, EndLine: lineNum})
		}
	}
}

func chunkText(content []byte, maxChars int) []Chunk {
	text := string(content)
	if len(text) <= maxChars {
		return []Chunk{{Content: strings.TrimSpace(text), StartLine: 1, EndLine: lineOf(text, len(text))}}
	}

	var chunks []Chunk
	overlap := maxChars / 10 // 10% overlap
	start := 0

	for start < len(text) {
		end := proseCutEnd(text, start, maxChars)
		chunks = append(chunks, Chunk{
			Content:   strings.TrimSpace(text[start:end]),
			StartLine: lineOf(text, start),
			EndLine:   lineOf(text, end-1),
		})
		if end >= len(text) {
			break
		}
		start = proseNextStart(text, start, end, overlap)
	}

	return chunks
}

// proseCutEnd picks the end offset of a prose chunk beginning at start. It aims
// for start+maxChars but prefers breaking at a newline past the halfway point,
// and otherwise snaps back to a rune boundary so a multi-byte character is never
// split.
func proseCutEnd(text string, start, maxChars int) int {
	end := start + maxChars
	if end >= len(text) {
		return len(text)
	}
	if nl := strings.LastIndex(text[start:end], "\n"); nl > maxChars/2 {
		return start + nl + 1
	}
	for end > start && !utf8.RuneStart(text[end]) {
		end--
	}
	return end
}

// proseNextStart computes the start offset of the next prose chunk, applying the
// overlap window on a rune boundary while guaranteeing forward progress.
func proseNextStart(text string, start, end, overlap int) int {
	next := end - overlap
	for next > start && next < len(text) && !utf8.RuneStart(text[next]) {
		next-- // keep the overlap window on a rune boundary too
	}
	if next <= start {
		next = end // guarantee forward progress even when overlap can't apply
	}
	return next
}

// lineOf returns the 1-based line number of the byte offset in text.
func lineOf(text string, offset int) int {
	if offset > len(text) {
		offset = len(text)
	}
	if offset < 0 {
		offset = 0
	}
	return 1 + strings.Count(text[:offset], "\n")
}

// splitRunes breaks s into pieces of at most maxBytes bytes each, never cutting
// a multi-byte UTF-8 rune.
func splitRunes(s string, maxBytes int) []string {
	var out []string
	for len(s) > maxBytes {
		end := maxBytes
		for end > 0 && !utf8.RuneStart(s[end]) {
			end--
		}
		if end == 0 { // a single rune wider than the budget: emit it whole
			end = maxBytes
			for end < len(s) && !utf8.RuneStart(s[end]) {
				end++
			}
		}
		out = append(out, s[:end])
		s = s[end:]
	}
	if len(s) > 0 {
		out = append(out, s)
	}
	return out
}

func scanLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[0 : i+1], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}
