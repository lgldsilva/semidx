package server

import (
	"net/http"
	"strconv"
)

const maxListLimit = 1000

// parseListParams reads optional limit and offset query parameters for list
// endpoints. limit=0 means no limit (return all rows). offset defaults to 0.
func parseListParams(r *http.Request) (limit, offset int) {
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
	if limit < 0 {
		limit = 0
	}
	if offset < 0 {
		offset = 0
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	return limit, offset
}
