// Package privacy classifies files that must not leave the machine for a
// cloud embedding provider (secrets, keys, credentials). The indexer uses it to
// route sensitive files to a local provider only.
package privacy

import (
	"path/filepath"
	"strings"
)

// sensitiveKeywords match any path segment (case-insensitive substring).
var sensitiveKeywords = []string{
	"env", "secret", "key", "password", "credential",
	"token", "auth", "config", "db", "database",
	"private", "pem", "jwks", "cert", "ssl",
}

// sensitiveExts match the file's extension exactly.
var sensitiveExts = map[string]bool{
	".env": true, ".pem": true, ".key": true,
	".conf": true, ".config": true,
}

// IsSensitive reports whether a file at the given path likely holds secrets or
// confidential configuration and so should never be sent to a cloud provider.
func IsSensitive(path string) bool {
	path = strings.ToLower(path)

	parts := strings.Split(path, string(filepath.Separator))
	for _, part := range parts {
		for _, kw := range sensitiveKeywords {
			if strings.Contains(part, kw) {
				return true
			}
		}
	}

	return sensitiveExts[filepath.Ext(path)]
}
