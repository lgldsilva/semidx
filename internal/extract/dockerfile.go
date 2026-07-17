package extract

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// dockerInstructions are the known Dockerfile/Containerfile instruction keywords.
var dockerInstructions = []string{
	"FROM", "RUN", "CMD", "ENV", "EXPOSE", "COPY", "ADD",
	"ENTRYPOINT", "VOLUME", "USER", "WORKDIR", "ARG", "LABEL", "HEALTHCHECK",
}

var dockerInstrRx = func() *regexp.Regexp {
	// Build a pattern like: ^(FROM|RUN|CMD|...)\b with multi-line mode so ^
	// matches start of line, not just start of string.
	return regexp.MustCompile(`(?m)^\s*(` + strings.Join(dockerInstructions, "|") + `)\b`)
}()

func init() {
	_ = RegisterName([]string{"Dockerfile", "Containerfile"}, extractDockerfile)
}

// extractDockerfile reads Dockerfile/Containerfile content (plain text) and
// optionally prepends instruction annotations (FROM, RUN, CMD, etc.) so semantic
// search finds build-stage keywords alongside the Dockerfile text.
func extractDockerfile(data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", ErrNotText
	}
	raw := string(data)

	var b strings.Builder
	seen := make(map[string]bool)
	for _, m := range dockerInstrRx.FindAllStringSubmatch(raw, -1) {
		if instr := m[1]; !seen[instr] {
			seen[instr] = true
			b.WriteString(instr)
			b.WriteByte('\n')
		}
	}

	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	b.WriteString(raw)
	return b.String(), nil
}
