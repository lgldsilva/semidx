package search

import (
	"encoding/json"
	"fmt"
	"io"
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
		if _, err := fmt.Fprintf(w, "--- Result %d (%s) ---\nFile: %s\n%s\n\n",
			i+1, matchLabel(resp.Keyword, r.Score), formatLoc(r.FilePath, r.StartLine, r.EndLine),
			truncatePreview(r.Content, preview)); err != nil {
			return err
		}
	}
	return nil
}

// GrepFormatter renders classic grep output: <fullpath>:<line>:<first line>.
// This is the format the sgrep workflow depends on. The line number comes from
// the stored chunk (SearchResult.StartLine), so no file read is needed.
type GrepFormatter struct {
	ProjectPath string
}

func (f GrepFormatter) Format(w io.Writer, resp *Response) error {
	for _, r := range resp.Results {
		full := filepath.Join(f.ProjectPath, r.FilePath)
		line := r.StartLine
		if line < 1 {
			line = 1
		}
		if _, err := fmt.Fprintf(w, "%s:%d:%s\n", full, line, firstNonEmptyLine(r.Content)); err != nil {
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

// matchLabel renders the per-result relevance label: cosine similarity as a
// percentage for a vector match, or "keyword match" when the response came from
// keyword search (its scores are a constant placeholder, not a similarity).
func matchLabel(keyword bool, score float64) string {
	if keyword {
		return "keyword match"
	}
	return fmt.Sprintf("%.0f%%", score*100)
}

// formatLoc renders "path:line" (or "path:start-end" for a multi-line chunk),
// clamping an unknown/zero start line to 1 to match the grep output contract.
func formatLoc(path string, start, end int) string {
	if start < 1 {
		start = 1
	}
	if end > start {
		return fmt.Sprintf("%s:%d-%d", path, start, end)
	}
	return fmt.Sprintf("%s:%d", path, start)
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
