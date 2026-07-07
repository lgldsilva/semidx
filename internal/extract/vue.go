package extract

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	scriptRE   = regexp.MustCompile(`(?s)<script[^>]*>(.*?)</script>`)
	templateRE = regexp.MustCompile(`(?s)<template[^>]*>(.*?)</template>`)
	htmlTagRE  = regexp.MustCompile(`<[^>]*>`)
)

func init() {
	Register(".vue", extractVue)
}

// extractVue extracts text from .vue Single File Components. It pulls the text
// content of <script> and <template> blocks, strips HTML tags from the template
// text, and returns them separated. If nothing matches, it returns the raw text.
func extractVue(data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", ErrNotText
	}
	text := string(data)

	var parts []string

	// Extract <script> blocks. The regex handles <script>, <script setup>,
	// <script lang="ts">, etc.
	for _, m := range scriptRE.FindAllStringSubmatch(text, -1) {
		if s := strings.TrimSpace(m[1]); s != "" {
			parts = append(parts, s)
		}
	}

	// Extract <template> blocks and strip HTML tags.
	for _, m := range templateRE.FindAllStringSubmatch(text, -1) {
		content := htmlTagRE.ReplaceAllString(m[1], " ")
		content = strings.Join(strings.Fields(content), " ")
		if s := strings.TrimSpace(content); s != "" {
			parts = append(parts, s)
		}
	}

	if len(parts) == 0 {
		return text, nil // fallback to raw text
	}
	return strings.Join(parts, "\n\n"), nil
}
