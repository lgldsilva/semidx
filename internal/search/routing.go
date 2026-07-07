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

// identifierRE matches a valid identifier: starts with a letter or underscore,
// followed by zero or more word characters, dots, or colons.
var identifierPattern = `^[a-zA-Z_][a-zA-Z0-9_.]*$`

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
