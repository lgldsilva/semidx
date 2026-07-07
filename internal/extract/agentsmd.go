package extract

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	mdHeaderRx = regexp.MustCompile(`(?m)^(#{1,6})\s+(.+)$`)
	mdToolRx   = regexp.MustCompile(`(?:^|\s)-(?:\s+\[)?([\w-]+)` + "`" + `?`)
	mdSkillRx  = regexp.MustCompile(`(?i)(?:skill|tool|command)[\s:]*[` + "`" + `]?(\w[\w./-]*)[` + "`" + `]?`)
)

func init() {
	RegisterName([]string{"AGENTS.md", "CLAUDE.md"}, extractAgentsMD)
}

// extractAgentsMD reads AGENTS.md / CLAUDE.md files (plain text) and optionally
// prepends metadata annotations: markdown headers, tool names, and skill
// references, so semantic search finds structural elements alongside the body.
func extractAgentsMD(data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", ErrNotText
	}
	raw := string(data)

	var b strings.Builder
	seen := make(map[string]bool)

	// Extract headers as structured annotations.
	for _, m := range mdHeaderRx.FindAllStringSubmatch(raw, -1) {
		level := len(m[1])
		title := strings.TrimSpace(m[2])
		key := "h" + m[1] + ":" + title
		if !seen[key] {
			seen[key] = true
			b.WriteString("header h")
			if level > 6 {
				level = 6
			}
			b.WriteByte('0' + byte(level))
			b.WriteString(": ")
			b.WriteString(title)
			b.WriteByte('\n')
		}
	}

	// Extract tool references: lines with `- tool_name` or `- [tool_name]`.
	for _, m := range mdToolRx.FindAllStringSubmatch(raw, -1) {
		name := m[1]
		if !seen["tool:"+name] {
			seen["tool:"+name] = true
			b.WriteString("tool ")
			b.WriteString(name)
			b.WriteByte('\n')
		}
	}

	// Extract skill references.
	for _, m := range mdSkillRx.FindAllStringSubmatch(raw, -1) {
		ref := m[1]
		if !seen["ref:"+ref] {
			seen["ref:"+ref] = true
			b.WriteString("ref ")
			b.WriteString(ref)
			b.WriteByte('\n')
		}
	}

	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	b.WriteString(raw)
	return b.String(), nil
}
