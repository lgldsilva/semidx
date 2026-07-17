package extract

import "unicode/utf8"

func init() {
	_ = RegisterName([]string{
		"LICENSE", "LICENSE.txt", "LICENSE.md",
		"NOTICE",
		"CHANGELOG", "CHANGELOG.md", "CHANGELOG.txt",
		"CONTRIBUTING", "CONTRIBUTING.md",
		"CODE_OF_CONDUCT", "CODE_OF_CONDUCT.md",
		"SECURITY", "SECURITY.md",
	}, extractLegal)
}

// extractLegal reads legal/project-documentation files (LICENSE, NOTICE,
// CHANGELOG, CONTRIBUTING, CODE_OF_CONDUCT, SECURITY) and returns the
// content verbatim as searchable text.
func extractLegal(data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", ErrNotText
	}
	return string(data), nil
}
