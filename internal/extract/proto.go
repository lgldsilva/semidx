package extract

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	protoServiceRx = regexp.MustCompile(`service\s+(\w+)`)
	protoMessageRx = regexp.MustCompile(`message\s+(\w+)`)
)

func init() {
	_ = Register(".proto", extractProto)
}

// extractProto reads .proto files (plain text) and optionally prepends
// "service/message" annotations so semantic search catches type names.
func extractProto(data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", ErrNotText
	}
	raw := string(data)

	var b strings.Builder
	seen := make(map[string]bool)
	for _, m := range protoServiceRx.FindAllStringSubmatch(raw, -1) {
		if name := m[1]; !seen[name] {
			seen[name] = true
			b.WriteString("service ")
			b.WriteString(name)
			b.WriteByte('\n')
		}
	}
	for _, m := range protoMessageRx.FindAllStringSubmatch(raw, -1) {
		if name := m[1]; !seen["message:"+name] {
			seen["message:"+name] = true
			b.WriteString("message ")
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
