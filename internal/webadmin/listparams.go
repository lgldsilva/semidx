package webadmin

import (
	"net/http"
	"strconv"
)

const maxAdminListLimit = 1000

func parseListParams(r *http.Request) (limit, offset int) {
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
	if limit < 0 {
		limit = 0
	}
	if offset < 0 {
		offset = 0
	}
	if limit > maxAdminListLimit {
		limit = maxAdminListLimit
	}
	return limit, offset
}
