package extract

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	jinjaCommentRE = regexp.MustCompile(`\{#.*?#\}`)
	jinjaStmtRE    = regexp.MustCompile(`\{%.*?%\}`)
	jinjaVarRE     = regexp.MustCompile(`\{\{.*?\}\}`)
)

func init() {
	_ = Register(".jinja", extractJinja)
	_ = Register(".jinja2", extractJinja)
	_ = Register(".j2", extractJinja)
}

// extractJinja strips Jinja template syntax ({{ }}, {% %}, {# #}) and returns
// the remaining text content with normalised whitespace.
func extractJinja(data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", ErrNotText
	}
	text := string(data)
	text = jinjaCommentRE.ReplaceAllString(text, "")
	text = jinjaStmtRE.ReplaceAllString(text, "")
	text = jinjaVarRE.ReplaceAllString(text, "")

	// Clean up blank lines left by removed directives.
	lines := strings.Split(text, "\n")
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return strings.Join(out, "\n"), nil
}
