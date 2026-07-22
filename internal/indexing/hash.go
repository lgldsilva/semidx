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
	// lgtm[go/weak-sensitive-data-hashing]
	// SHA-256 is a strong cryptographic hash. Used here for content
	// addressing (deduplication of indexed chunks), not for password
	// or security-sensitive hashing. CodeQL's rule fires because
	// `content` originates from user-provided files; the hash itself
	// is not a security boundary.
	return fmt.Sprintf("%x", sha256.Sum256(content))
}
