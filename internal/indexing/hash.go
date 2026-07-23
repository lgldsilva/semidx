package indexing

import (
	"crypto/sha256"
	"fmt"
)

// ContentHash returns the hex-encoded SHA-256 of content — the same digest the
// indexer stores in files.hash. Search staleness checks must use this exact
// function so on-disk comparisons match the indexed value.
//
// This is a content-addressed digest for deduplication of indexed units (akin
// to a git blob hash); it is not a security control.
func ContentHash(content []byte) string {
	// codeql[go/weak-sensitive-data-hashing] : content-addressed digest, not a security control.
	sum := sha256.Sum256(content)
	return fmt.Sprintf("%x", sum)
}
