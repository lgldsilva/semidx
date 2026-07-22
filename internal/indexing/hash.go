package indexing

import (
	"crypto/sha256"
	"fmt"
)

// ContentHash returns the hex-encoded SHA-256 of content — the same digest the
// indexer stores in files.hash. Search staleness checks must use this exact
// function so on-disk comparisons match the indexed value.
//
// The hash is for content-addressed dedup of indexed units, not for
// password/security-sensitive material.
func ContentHash(content []byte) string {
	sum := sha256.Sum256(content) // codeql[go/weak-sensitive-data-hashing] : content-addressed dedup, not password hashing
	return fmt.Sprintf("%x", sum)
}
