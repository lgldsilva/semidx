package extract

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	hbsTripleRE = regexp.MustCompile(`\{\{\{.*?\}\}\}`)
	hbsDoubleRE = regexp.MustCompile(`\{\{.*?\}\}`)
)

func init() {
	_ = Register(".hbs", extractHandlebars)
	_ = Register(".handlebars", extractHandlebars)
}

// extractHandlebars strips Handlebars/Mustache syntax ({{ }}, {{{ }}}, {{# }},
// {{/ }}, {{> }}) and returns the remaining text content.
func extractHandlebars(data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", ErrNotText
	}
	text := string(data)
	// Strip triple-curlies first so they don't leave a trailing }.
	text = hbsTripleRE.ReplaceAllString(text, "")
	text = hbsDoubleRE.ReplaceAllString(text, "")

	// Clean up blank lines left by removed tags.
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
