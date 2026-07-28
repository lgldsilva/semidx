// Package indexfingerprint creates stable identifiers for the indexed corpus of
// a project. This corpus fingerprint is an evaluation comparability guard; it
// is not the versioned embedding/chunker compatibility descriptor planned for
// the search runtime.
package indexfingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// ComputeCorpus returns a deterministic SHA-256 fingerprint for one
// project corpus. Changing identity, commit, a path, or its indexed hash
// changes the result; checkout location and map iteration order never do.
func ComputeCorpus(identity, commit string, files map[string]string) string {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	h := sha256.New()
	writeField := func(value string) {
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{0})
	}
	writeField(strings.TrimSpace(identity))
	writeField(strings.TrimSpace(commit))
	for _, path := range paths {
		writeField(path)
		writeField(files[path])
	}
	return hex.EncodeToString(h.Sum(nil))
}
