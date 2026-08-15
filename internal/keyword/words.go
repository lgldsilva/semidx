// Package keyword provides shared keyword-search helpers used by both storage
// backends (PostgreSQL ILIKE and SQLite FTS5).
package keyword

import "strings"

const (
	maxSearchTerms    = 20
	maxSearchWordSize = 256
)

// FilterSearchWords filters and normalises query words for keyword search:
// removes terms shorter than 3 characters, drops oversized terms, deduplicates
// case-insensitively, and caps at 20 terms to prevent wasteful scans and DoS via
// query explosion. Returns nil if no valid words remain.
func FilterSearchWords(queryText string) []string {
	words := strings.Fields(queryText)
	if len(words) == 0 {
		return nil
	}
	filtered := make([]string, 0, min(len(words), maxSearchTerms))
	seen := make(map[string]struct{}, min(len(words), maxSearchTerms))
	for _, w := range words {
		if len(w) < 3 || len(w) > maxSearchWordSize {
			continue
		}
		key := strings.ToLower(w)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, w)
		if len(filtered) == maxSearchTerms {
			break
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
