package webadmin

import (
	"net/http/httptest"
	"testing"
)

func TestParseListParams(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantLimit  int
		wantOffset int
	}{
		{name: "defaults", query: "", wantLimit: 0, wantOffset: 0},
		{name: "normal", query: "limit=50&offset=10", wantLimit: 50, wantOffset: 10},
		{name: "negative_limit", query: "limit=-1", wantLimit: 0, wantOffset: 0},
		{name: "negative_offset", query: "offset=-5", wantLimit: 0, wantOffset: 0},
		{name: "cap_limit", query: "limit=5000", wantLimit: maxAdminListLimit, wantOffset: 0},
		{name: "invalid_numbers", query: "limit=abc&offset=xyz", wantLimit: 0, wantOffset: 0},
		{name: "zero_values", query: "limit=0&offset=0", wantLimit: 0, wantOffset: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/admin/api/projects?"+tc.query, nil)
			limit, offset := parseListParams(r)
			if limit != tc.wantLimit || offset != tc.wantOffset {
				t.Fatalf("parseListParams(%q) = (%d,%d), want (%d,%d)",
					tc.query, limit, offset, tc.wantLimit, tc.wantOffset)
			}
		})
	}
}
