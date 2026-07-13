// Package search unifies the semantic-search flow.
package search

import "strings"

// QueryType classifies a search query for smart routing.
type QueryType int

const (
	// QueryUnknown is the zero value (unclassified).
	QueryUnknown QueryType = iota
	// QueryIdentifier matches camelCase, snake_case, or dotted names (no spaces).
	QueryIdentifier
	// QueryPath matches strings containing "/" (file paths).
	QueryPath
	// QueryExact matches double-quoted exact strings.
	QueryExact
	// QueryNaturalLanguage is the default for anything with spaces.
	QueryNaturalLanguage
)

// ClassifyQuery heuristically classifies a query string into a QueryType.
func ClassifyQuery(query string) QueryType {
	if query == "" {
		return QueryUnknown
	}

	// Quoted → exact match.
	if strings.HasPrefix(query, `"`) && strings.HasSuffix(query, `"`) && len(query) >= 2 {
		return QueryExact
	}

	// Contains "/" → path.
	if strings.Contains(query, "/") {
		return QueryPath
	}

	// Contains spaces → natural language.
	if strings.ContainsAny(query, " \t\n") {
		return QueryNaturalLanguage
	}

	// Check identifier pattern (letter/underscore start, no spaces, only word chars/dots).
	if isIdentifier(query) {
		return QueryIdentifier
	}

	return QueryNaturalLanguage
}

// isIdentifier checks whether s looks like a code identifier.
func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' {
			continue
		}
		if i > 0 && ((r >= '0' && r <= '9') || r == '.') {
			continue
		}
		return false
	}
	return true
}

// RoutesToKeyword reports whether a query should skip embedding and use the
// keyword/FTS path directly (identifiers, paths, exact quoted strings).
func RoutesToKeyword(qt QueryType) bool {
	switch qt {
	case QueryIdentifier, QueryPath, QueryExact:
		return true
	default:
		return false
	}
}

// KeywordQueryForRouting returns the text to pass to keyword search after
// routing. Exact queries lose their surrounding quotes.
func KeywordQueryForRouting(query string, qt QueryType) string {
	if qt == QueryExact && len(query) >= 2 {
		return query[1 : len(query)-1]
	}
	return query
}

// String returns a human-readable label for the query type.
func (qt QueryType) String() string {
	switch qt {
	case QueryUnknown:
		return "unknown"
	case QueryIdentifier:
		return "identifier"
	case QueryPath:
		return "path"
	case QueryExact:
		return "exact"
	case QueryNaturalLanguage:
		return "natural_language"
	default:
		return "unknown"
	}
}
