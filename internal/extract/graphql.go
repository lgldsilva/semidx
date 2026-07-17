package extract

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var graphqlDefRx = regexp.MustCompile(`(?:type|input|enum|query|mutation)\s+(\w+)`)

func init() {
	_ = Register(".graphql", extractGraphQL)
	_ = Register(".gql", extractGraphQL)
}

// extractGraphQL reads .graphql/.gql files (plain text) and optionally prepends
// "type/input/enum/query/mutation" annotations so semantic search finds schema
// type names alongside the query text.
func extractGraphQL(data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", ErrNotText
	}
	raw := string(data)

	var b strings.Builder
	seen := make(map[string]bool)
	for _, m := range graphqlDefRx.FindAllStringSubmatch(raw, -1) {
		kw := extractKeyword(m[0], m[1])
		if name := m[1]; !seen[kw+":"+name] {
			seen[kw+":"+name] = true
			b.WriteString(kw)
			b.WriteByte(' ')
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

// extractKeyword returns the keyword prefix from a GraphQL type definition match.
func extractKeyword(match, name string) string {
	for _, kw := range []string{"type", "input", "enum", "query", "mutation"} {
		if strings.HasPrefix(match, kw) {
			return kw
		}
	}
	return "type"
}
