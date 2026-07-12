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
	many := strings.Fields(strings.Repeat("word ", 25))
	if len(keyword.FilterSearchWords(strings.Join(many, " "))) != 20 {
		t.Fatal("expected cap at 20 terms")
	}
}
