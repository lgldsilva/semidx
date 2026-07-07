package extract

import (
	"strings"
	"unicode/utf8"
)

func init() {
	Register(".rst", extractRST)
}

// extractRST strips reStructuredText directives (lines starting with ".. " and
// ".." bare comments) and their indented continuation blocks, while keeping
// headings, body text, and non-directive content.
func extractRST(data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", ErrNotText
	}

	lines := strings.Split(string(data), "\n")
	var out []string
	inDirective := false
	directiveIndent := -1

	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		indent := len(line) - len(trimmed)

		if inDirective {
			// Continuation lines are indented relative to the directive marker.
			if indent > directiveIndent && trimmed != "" {
				continue
			}
			inDirective = false
		}

		// Match directive/comment markers: ".. name:" or bare "..".
		if strings.HasPrefix(trimmed, ".. ") || trimmed == ".." {
			inDirective = true
			// Continuation is at least one space deeper than the directive marker.
			directiveIndent = indent + 1
			continue
		}

		out = append(out, line)
	}

	result := strings.TrimSpace(strings.Join(out, "\n"))
	if result == "" {
		return string(data), nil // fallback to raw if stripping removed everything
	}
	return result, nil
}
