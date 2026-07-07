package extract

import "unicode/utf8"

func init() {
	Register(".toml", extractTOML)
}

// extractTOML passes through TOML text as-is after UTF-8 validation.
func extractTOML(data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", ErrNotText
	}
	return string(data), nil
}
