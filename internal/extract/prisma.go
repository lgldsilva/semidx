package extract

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	prismaModelRx      = regexp.MustCompile(`model\s+(\w+)`)
	prismaEnumRx       = regexp.MustCompile(`enum\s+(\w+)`)
	prismaDatasourceRx = regexp.MustCompile(`datasource\s+(\w+)`)
	prismaGeneratorRx  = regexp.MustCompile(`generator\s+(\w+)`)
)

func init() {
	_ = Register(".prisma", extractPrisma)
}

// extractPrisma reads .prisma files (plain text) and optionally prepends
// "model/datasource/generator/enum" annotations so semantic search finds schema
// declarations alongside the DSL text.
func extractPrisma(data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", ErrNotText
	}
	raw := string(data)

	var b strings.Builder
	seen := make(map[string]bool)

	for _, name := range extractNames(prismaModelRx, raw, "model") {
		if !seen["model:"+name] {
			seen["model:"+name] = true
			b.WriteString("model ")
			b.WriteString(name)
			b.WriteByte('\n')
		}
	}
	for _, name := range extractNames(prismaEnumRx, raw, "enum") {
		if !seen["enum:"+name] {
			seen["enum:"+name] = true
			b.WriteString("enum ")
			b.WriteString(name)
			b.WriteByte('\n')
		}
	}
	for _, name := range extractNames(prismaDatasourceRx, raw, "datasource") {
		if !seen["datasource:"+name] {
			seen["datasource:"+name] = true
			b.WriteString("datasource ")
			b.WriteString(name)
			b.WriteByte('\n')
		}
	}
	for _, name := range extractNames(prismaGeneratorRx, raw, "generator") {
		if !seen["generator:"+name] {
			seen["generator:"+name] = true
			b.WriteString("generator ")
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

// extractNames returns unique capture-group values from a regex that matches
// definition patterns like "model Foo".
func extractNames(rx *regexp.Regexp, raw string, _ string) []string {
	seen := make(map[string]bool)
	var names []string
	for _, m := range rx.FindAllStringSubmatch(raw, -1) {
		if name := m[1]; !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}
