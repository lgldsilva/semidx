package keyword_test

import (
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/keyword"
)

func TestFilterSearchWords(t *testing.T) {
	if got := keyword.FilterSearchWords(""); got != nil {
		t.Fatalf("empty = %v, want nil", got)
	}
	if got := keyword.FilterSearchWords("a bb"); got != nil {
		t.Fatalf("short words = %v, want nil", got)
	}
	got := keyword.FilterSearchWords("auth middleware handler")
	if len(got) != 3 {
		t.Fatalf("got %v", got)
	}
	many := make([]string, 25)
	for i := range many {
		many[i] = "word" + strings.Repeat("x", i+1)
	}
	if len(keyword.FilterSearchWords(strings.Join(many, " "))) != 20 {
		t.Fatal("expected cap at 20 terms")
	}
	if got := keyword.FilterSearchWords("Auth auth TOKEN token"); len(got) != 2 {
		t.Fatalf("deduplicated terms = %v, want two", got)
	}
	if got := keyword.FilterSearchWords("safe " + strings.Repeat("x", 257)); len(got) != 1 || got[0] != "safe" {
		t.Fatalf("oversized term filtering = %v, want [safe]", got)
	}
}
