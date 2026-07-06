// Package keyword provides shared keyword-search helpers used by both storage
// backends (PostgreSQL ILIKE and SQLite FTS5).
package keyword

import "strings"

// FilterSearchWords filters and normalises query words for keyword search:
// removes terms shorter than 3 characters and caps at 20 terms to prevent
// wasteful scans and DoS via query explosion. Returns nil if no valid words remain.
func FilterSearchWords(queryText string) []string {
	words := strings.Fields(queryText)
	if len(words) == 0 {
		return nil
	}
	filtered := words[:0]
	for _, w := range words {
		if len(w) >= 3 {
			filtered = append(filtered, w)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	if len(filtered) > 20 {
		filtered = filtered[:20]
	}
	return filtered
}
