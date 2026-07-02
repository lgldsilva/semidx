package search

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Formatter renders a search Response to a writer.
type Formatter interface {
	Format(w io.Writer, resp *Response) error
}

// HumanFormatter renders numbered result blocks with a content preview. Preview
// is the max preview length in bytes (<= 0 means 500).
type HumanFormatter struct {
	Preview int
}

func (f HumanFormatter) Format(w io.Writer, resp *Response) error {
	preview := f.Preview
	if preview <= 0 {
		preview = 500
	}
	for i, r := range resp.Results {
		if _, err := fmt.Fprintf(w, "--- Result %d (score: %.4f) ---\nFile: %s\n%s\n\n",
			i+1, r.Score, r.FilePath, truncatePreview(r.Content, preview)); err != nil {
			return err
		}
	}
	return nil
}

// GrepFormatter renders classic grep output: <fullpath>:<line>:<first line>.
// This is the format the sgrep workflow depends on. FindLine resolves the line
// number for a hit; when nil it reads the file on disk (see findLineInFile).
type GrepFormatter struct {
	ProjectPath string
	FindLine    func(fullPath, content string) int
}

func (f GrepFormatter) Format(w io.Writer, resp *Response) error {
	find := f.FindLine
	if find == nil {
		find = findLineInFile
	}
	for _, r := range resp.Results {
		full := filepath.Join(f.ProjectPath, r.FilePath)
		if _, err := fmt.Fprintf(w, "%s:%d:%s\n", full, find(full, r.Content), firstNonEmptyLine(r.Content)); err != nil {
			return err
		}
	}
	return nil
}

// JSONFormatter renders machine-readable output.
type JSONFormatter struct{}

func (JSONFormatter) Format(w io.Writer, resp *Response) error {
	type row struct {
		File    string  `json:"file"`
		Score   float64 `json:"score"`
		Content string  `json:"content"`
	}
	out := struct {
		Project  string `json:"project"`
		Model    string `json:"model"`
		Fallback bool   `json:"fallback"`
		Results  []row  `json:"results"`
	}{Model: resp.Model, Fallback: resp.Fallback, Results: []row{}}
	if resp.Project != nil {
		out.Project = resp.Project.Name
	}
	for _, r := range resp.Results {
		out.Results = append(out.Results, row{File: r.FilePath, Score: r.Score, Content: r.Content})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// firstNonEmptyLine returns the first non-blank line of content, trimmed.
func firstNonEmptyLine(content string) string {
	for _, l := range strings.Split(content, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
}

// truncatePreview trims s and caps it at max bytes without splitting a rune.
func truncatePreview(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	end := max
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "..."
}

// findLineInFile locates the 1-based line of a chunk's first non-empty line by
// reading the file. Returns 1 when the file or line can't be found. (This is the
// on-demand line mapping the PoC used; the server will supply line numbers
// directly in a later phase.)
func findLineInFile(fullPath, content string) int {
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return 1
	}
	first := firstNonEmptyLine(content)
	if first == "" {
		return 1
	}
	idx := strings.Index(string(data), first)
	if idx < 0 {
		return 1
	}
	line := 1
	for i := 0; i < idx; i++ {
		if data[i] == '\n' {
			line++
		}
	}
	return line
}
