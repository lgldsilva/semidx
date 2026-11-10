// Package privacy classifies files that must not leave the machine for a
// cloud embedding provider (secrets, keys, credentials). The indexer uses it to
// route sensitive files to a local provider only.
package privacy

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Mode is the project-level data routing policy persisted by the server.
type Mode string

const (
	Cloud  Mode = "cloud"
	Hybrid Mode = "hybrid"
	Edge   Mode = "edge"
)

// NormalizeMode validates a project policy. Hybrid preserves the historical
// semidx behavior and is the compatibility default.
func NormalizeMode(value string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(value))) {
	case Cloud:
		return Cloud, nil
	case Hybrid, "":
		return Hybrid, nil
	case Edge:
		return Edge, nil
	default:
		return "", fmt.Errorf("privacy mode must be cloud, hybrid, or edge")
	}
}

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
	".p12": true, ".pfx": true, ".jks": true, ".keystore": true,
	".tfvars": true, ".tfstate": true,
}

// sensitiveBaseNames are well-known credential files whose names carry no
// keyword (SSH private keys, package-manager and database auth files).
var sensitiveBaseNames = map[string]bool{
	"id_rsa": true, "id_dsa": true, "id_ecdsa": true, "id_ed25519": true,
	".npmrc": true, ".netrc": true, "_netrc": true, ".pgpass": true,
	".pypirc": true, ".htpasswd": true, ".dockercfg": true, "kubeconfig": true,
}

// IsSensitive reports whether a file at the given path likely holds secrets or
// confidential configuration and so should never be sent to a cloud provider.
func IsSensitive(path string) bool {
	path = strings.ToLower(path)

	parts := strings.Split(path, string(filepath.Separator))
	for _, part := range parts {
		for _, kw := range sensitiveKeywords {
			if segmentMatches(part, kw) {
				return true
			}
		}
	}

	return sensitiveBaseNames[filepath.Base(path)] || sensitiveExts[filepath.Ext(path)]
}

// segmentMatches reports whether a path segment contains kw as a whole
// sub-segment, singular or plural (e.g. "api_key" and "keys" match "key", but
// "keyboard" does not).
func segmentMatches(part, kw string) bool {
	for _, token := range strings.FieldsFunc(part, func(r rune) bool { return r == '.' || r == '_' || r == '-' }) {
		if token == kw || token == kw+"s" {
			return true
		}
	}
	return false
}
