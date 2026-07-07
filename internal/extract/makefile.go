package extract

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var targetRx = regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_./-]*)\s*:`)

func init() {
	RegisterName([]string{"Makefile", "makefile", "GNUmakefile"}, extractMakefile)
}

// extractMakefile reads Makefile content (plain text) and optionally prepends
// target names extracted via regex: lines matching `^target:` are annotated
// as "target <name>" so semantic search finds build targets.
func extractMakefile(data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", ErrNotText
	}
	raw := string(data)

	var b strings.Builder
	seen := make(map[string]bool)
	for _, m := range targetRx.FindAllStringSubmatch(raw, -1) {
		if name := m[1]; !seen[name] {
			seen[name] = true
			b.WriteString("target ")
			b.WriteString(name)
			b.WriteByte('\n')
		}
	}

	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	b.WriteString(raw)
	return b.String(), nil
}
