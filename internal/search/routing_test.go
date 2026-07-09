package search

import "testing"

func TestClassifyQueryEmpty(t *testing.T) {
	t.Parallel()
	if got := ClassifyQuery(""); got != QueryUnknown {
		t.Fatalf("ClassifyQuery(\"\") = %v, want QueryUnknown", got)
	}
}

func TestClassifyQueryExact(t *testing.T) {
	t.Parallel()
	if got := ClassifyQuery(`"exact match"`); got != QueryExact {
		t.Fatalf("got %v, want QueryExact", got)
	}
	if got := ClassifyQuery(`""`); got != QueryExact {
		t.Fatalf("got %v, want QueryExact", got)
	}
}

func TestClassifyQueryPath(t *testing.T) {
	t.Parallel()
	if got := ClassifyQuery("src/main.go"); got != QueryPath {
		t.Fatalf("got %v, want QueryPath", got)
	}
	if got := ClassifyQuery("/absolute/path"); got != QueryPath {
		t.Fatalf("got %v, want QueryPath", got)
	}
}

func TestClassifyQueryNaturalLanguage(t *testing.T) {
	t.Parallel()
	if got := ClassifyQuery("find all tokens"); got != QueryNaturalLanguage {
		t.Fatalf("got %v, want QueryNaturalLanguage", got)
	}
	if got := ClassifyQuery("a\tb"); got != QueryNaturalLanguage {
		t.Fatalf("got %v, want QueryNaturalLanguage", got)
	}
	if got := ClassifyQuery("a\nb"); got != QueryNaturalLanguage {
		t.Fatalf("got %v, want QueryNaturalLanguage", got)
	}
}

func TestClassifyQueryIdentifier(t *testing.T) {
	t.Parallel()
	for _, q := range []string{"camelCase", "snake_case", "UPPER", "foo.bar.Baz", "_private", "with123"} {
		if got := ClassifyQuery(q); got != QueryIdentifier {
			t.Errorf("ClassifyQuery(%q) = %v, want QueryIdentifier", q, got)
		}
	}
}

func TestClassifyQueryFallbackNatural(t *testing.T) {
	t.Parallel()
	// Starts with digit, hyphen, etc. — not identifier.
	if got := ClassifyQuery("123abc"); got != QueryNaturalLanguage {
		t.Fatalf("got %v, want QueryNaturalLanguage", got)
	}
	if got := ClassifyQuery("foo-bar"); got != QueryNaturalLanguage {
		t.Fatalf("got %v, want QueryNaturalLanguage", got)
	}
}

func TestIsIdentifier(t *testing.T) {
	t.Parallel()
	tests := []struct {
		s    string
		want bool
	}{
		{"", false},
		{"hello", true},
		{"HelloWorld", true},
		{"hello_world", true},
		{"hello.world.Foo", true},
		{"hello123", true},
		{"0prefix", false},
		{"foo-bar", false},
		{"foo bar", false},
		{"123", false},
	}
	for _, tt := range tests {
		got := isIdentifier(tt.s)
		if got != tt.want {
			t.Errorf("isIdentifier(%q) = %v, want %v", tt.s, got, tt.want)
		}
	}
}

func TestQueryTypeString(t *testing.T) {
	t.Parallel()
	constants := map[QueryType]string{
		QueryUnknown:         "unknown",
		QueryIdentifier:      "identifier",
		QueryPath:            "path",
		QueryExact:           "exact",
		QueryNaturalLanguage: "natural_language",
		QueryType(999):       "unknown",
	}
	for qt, want := range constants {
		if got := qt.String(); got != want {
			t.Errorf("QueryType(%d).String() = %q, want %q", qt, got, want)
		}
	}
}
