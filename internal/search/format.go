package search

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/lgldsilva/semidx/internal/store"
)

// Formatter renders a search Response to a writer.
type Formatter interface {
	Format(w io.Writer, resp *Response) error
}

// HumanFormatter renders numbered result blocks with a content preview.
// Preview is the max preview length in bytes (<= 0 means 500). LineNumbers
// controls whether line numbers are prepended to each content line.
type HumanFormatter struct {
	Preview    int
	NoLineNums bool // suppress per-line line numbers in content output
	LineNumPad int  // padding width for line numbers (<= 0 means auto: 4)
}

func (f HumanFormatter) Format(w io.Writer, resp *Response) error {
	preview := f.Preview
	if preview <= 0 {
		preview = 500
	}
	pad := f.LineNumPad
	if pad <= 0 {
		pad = 4
	}
	for i, r := range resp.Results {
		if err := f.formatResult(w, i, r, resp.Keyword, preview, pad); err != nil {
			return err
		}
	}
	return nil
}

// formatResult renders a single result block (header + file:line + content).
// Extracted from Format to keep cognitive complexity below the SonarQube gate.
func (f HumanFormatter) formatResult(w io.Writer, i int, r store.SearchResult, keyword bool, preview, pad int) error {
	label := matchLabel(keyword, r.Score)
	if r.Content == "" && r.FilePath != "" {
		label = "(graph match)"
	}
	if _, err := fmt.Fprintf(w, "--- Result %d (%s) ---\n", i+1, label); err != nil {
		return err
	}
	loc := formatLoc(r.FilePath, r.StartLine, r.EndLine)
	if _, err := fmt.Fprintf(w, "File: %s\n", loc); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Score: %s\n", humanScore(keyword, r.Score)); err != nil {
		return err
	}
	content := truncatePreview(r.Content, preview)
	if f.NoLineNums || r.StartLine < 1 || r.EndLine < r.StartLine {
		if _, err := fmt.Fprintf(w, "%s\n\n", content); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(w, "%s\n\n", prefixLineNumbers(content, r.StartLine, pad)); err != nil {
			return err
		}
	}
	return nil
}

func humanScore(keyword bool, score float64) string {
	if keyword {
		return "keyword match"
	}
	return fmt.Sprintf("%.3f (%.0f%%)", score, score*100)
}

// prefixLineNumbers prepends padded line numbers to each line of content,
// starting at startLine. The format is "NNNN│ " where NNNN is right-justified.
func prefixLineNumbers(content string, startLine, padWidth int) string {
	lines := strings.Split(content, "\n")
	// Trim trailing empty line from split (content never ends with \n after trim)
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	maxLine := startLine + len(lines) - 1
	// Compute padding based on the widest line number
	width := padWidth
	if w := len(fmt.Sprintf("%d", maxLine)); w > width {
		width = w
	}
	var b strings.Builder
	b.Grow(len(content) + len(lines)*(width+3)) // "NNNN│ " per line
	for i, l := range lines {
		fmt.Fprintf(&b, "%*d│ %s\n", width, startLine+i, l)
	}
	return strings.TrimRight(b.String(), "\n")
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
