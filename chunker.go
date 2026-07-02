package main

import (
	"bufio"
	"bytes"
	"path/filepath"
	"strings"
)

type Chunk struct {
	Content string
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

func ShouldIndex(path string) bool {
	// Split the path and check if any directory in the path is ignored
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

	if codeExts[ext] || textExts[ext] {
		return true
	}

	return false
}

func ChunkFile(path string, content []byte, maxChars int) []Chunk {
	ext := strings.ToLower(filepath.Ext(path))
	isCode := codeExts[ext]

	if isCode {
		return chunkCode(content, maxChars)
	}
	return chunkText(content, maxChars)
}

func chunkCode(content []byte, maxChars int) []Chunk {
	// Simple chunking: split by blank lines, then merge into chunks up to maxChars
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Split(scanLines)

	var chunks []Chunk
	var current strings.Builder

	flush := func() {
		if current.Len() == 0 {
			return
		}
		chunks = append(chunks, Chunk{Content: strings.TrimSpace(current.String())})
		current.Reset()
	}

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "" && current.Len() > 0 {
			flush()
			continue
		}

		if current.Len()+len(line)+1 > maxChars && current.Len() > 0 {
			flush()
		}

		current.WriteString(line)
		current.WriteString("\n")
	}

	flush()

	// If no chunks (e.g. file without blank lines), split by line count
	if len(chunks) == 0 && len(content) > 0 {
		return chunkText(content, maxChars)
	}

	return chunks
}

func chunkText(content []byte, maxChars int) []Chunk {
	text := string(content)
	if len(text) <= maxChars {
		return []Chunk{{Content: strings.TrimSpace(text)}}
	}

	var chunks []Chunk
	overlap := maxChars / 10 // 10% overlap
	start := 0

	for start < len(text) {
		end := start + maxChars
		if end > len(text) {
			end = len(text)
		}

		// Try to break at newline
		if end < len(text) {
			if nl := strings.LastIndex(text[start:end], "\n"); nl > maxChars/2 {
				end = start + nl + 1
			}
		}

		chunks = append(chunks, Chunk{Content: strings.TrimSpace(text[start:end])})
		if end >= len(text) {
			break
		}
		start = end - overlap
		if start >= end {
			break
		}
	}

	return chunks
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

// IsSensitive verifica se o arquivo é confidencial/segredo.
func IsSensitive(path string) bool {
	path = strings.ToLower(path)
	
	// Divide o caminho em partes para verificar diretórios sensíveis
	parts := strings.Split(path, string(filepath.Separator))
	sensitiveKeywords := []string{
		"env", "secret", "key", "password", "credential",
		"token", "auth", "config", "db", "database",
		"private", "pem", "jwks", "cert", "ssl",
	}
	
	for _, part := range parts {
		for _, kw := range sensitiveKeywords {
			if strings.Contains(part, kw) {
				return true
			}
		}
	}

	// Verifica extensões sensíveis
	ext := filepath.Ext(path)
	sensitiveExts := map[string]bool{
		".env": true, ".pem": true, ".key": true,
		".conf": true, ".config": true,
	}
	
	if sensitiveExts[ext] {
		return true
	}
	
	return false
}
