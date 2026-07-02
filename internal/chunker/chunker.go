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
func ChunkFile(path string, content []byte, maxChars int) []Chunk {
	ext := strings.ToLower(filepath.Ext(path))
	if codeExts[ext] {
		return chunkCode(content, maxChars)
	}
	return chunkText(content, maxChars)
}

func chunkCode(content []byte, maxChars int) []Chunk {
	// Split by blank lines, then merge runs of lines into chunks up to maxChars,
	// tracking the 1-based source line range of each chunk.
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Split(scanLines)

	var chunks []Chunk
	var current strings.Builder
	curStart, curEnd := 0, 0
	lineNum := 0

	flush := func() {
		if current.Len() == 0 {
			return
		}
		chunks = append(chunks, Chunk{Content: strings.TrimSpace(current.String()), StartLine: curStart, EndLine: curEnd})
		current.Reset()
	}

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "" && current.Len() > 0 {
			flush()
			continue
		}

		// A single line longer than the budget can't fit any chunk: flush what
		// we have, then hard-split the line on rune boundaries (all pieces map
		// to that one source line).
		if len(line) > maxChars {
			flush()
			for _, piece := range splitRunes(line, maxChars) {
				if p := strings.TrimSpace(piece); p != "" {
					chunks = append(chunks, Chunk{Content: p, StartLine: lineNum, EndLine: lineNum})
				}
			}
			continue
		}

		if current.Len()+len(line)+1 > maxChars && current.Len() > 0 {
			flush()
		}
		if current.Len() == 0 {
			curStart = lineNum
		}
		curEnd = lineNum
		current.WriteString(line)
		current.WriteString("\n")
	}

	flush()

	// Files without blank lines produce no chunks above; fall back to prose split.
	if len(chunks) == 0 && len(content) > 0 {
		return chunkText(content, maxChars)
	}

	return chunks
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
		end := start + maxChars
		if end > len(text) {
			end = len(text)
		}

		// Prefer breaking at a newline past the halfway point; otherwise snap the
		// cut back to a rune boundary so a multi-byte character is never split.
		if end < len(text) {
			if nl := strings.LastIndex(text[start:end], "\n"); nl > maxChars/2 {
				end = start + nl + 1
			} else {
				for end > start && !utf8.RuneStart(text[end]) {
					end--
				}
			}
		}

		chunks = append(chunks, Chunk{
			Content:   strings.TrimSpace(text[start:end]),
			StartLine: lineOf(text, start),
			EndLine:   lineOf(text, end-1),
		})
		if end >= len(text) {
			break
		}

		next := end - overlap
		for next > start && next < len(text) && !utf8.RuneStart(text[next]) {
			next-- // keep the overlap window on a rune boundary too
		}
		if next <= start {
			next = end // guarantee forward progress even when overlap can't apply
		}
		start = next
	}

	return chunks
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
