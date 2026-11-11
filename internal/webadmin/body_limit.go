package webadmin

import (
	"net/http"
	"strings"

	"github.com/lgldsilva/semidx/internal/indexing"
)

// Request-body caps for /admin. Every handler gets a bound; ingest routes get
// room for their documented payloads (50 × 512 KiB files as JSON, or a 20 MiB
// zip plus multipart framing).
const (
	adminLoginMaxBody   int64 = 4 << 10
	adminDefaultMaxBody int64 = 1 << 20
	adminBatchMaxBody   int64 = 64 << 20
	adminArchiveMaxBody int64 = adminIngestMaxZipBytes + 1<<20
)

// adminBodyLimit returns the body cap for an /admin request path.
func adminBodyLimit(p string) int64 {
	switch {
	case p == "/admin/api/login":
		return adminLoginMaxBody
	case strings.HasSuffix(p, "/files/batch"):
		return adminBatchMaxBody
	case strings.HasSuffix(p, "/files/archive"):
		return adminArchiveMaxBody
	}
	return adminDefaultMaxBody
}

// limitBodies wraps every /admin request body in http.MaxBytesReader.
func limitBodies(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, adminBodyLimit(r.URL.Path))
		}
		next.ServeHTTP(w, r)
	})
}

// SetIndexerOpts applies the server's indexing caps (file size, chunk and
// file counts) to admin-SPA ingest, so browser uploads obey the same limits
// as push and background jobs.
func (a *Admin) SetIndexerOpts(o indexing.IndexerOpts) { a.indexerOpts = o }
