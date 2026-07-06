package server

import (
	"net/http/httptest"
	"testing"
)

func TestParseListParams(t *testing.T) {
	r := httptest.NewRequest("GET", "/?limit=50&offset=10", nil)
	limit, offset := parseListParams(r)
	if limit != 50 || offset != 10 {
		t.Fatalf("limit=%d offset=%d", limit, offset)
	}
	r = httptest.NewRequest("GET", "/?limit=-1&offset=-5", nil)
	limit, offset = parseListParams(r)
	if limit != 0 || offset != 0 {
		t.Fatalf("negative = limit %d offset %d", limit, offset)
	}
	r = httptest.NewRequest("GET", "/?limit=5000", nil)
	limit, _ = parseListParams(r)
	if limit != maxListLimit {
		t.Fatalf("limit capped at %d, got %d", maxListLimit, limit)
	}
}
