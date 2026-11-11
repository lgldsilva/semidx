package server

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

// Regression: request-duration labels used the raw URL path and method, so
// every 404 probe or custom verb minted a new Prometheus series (unbounded
// cardinality). Labels must be the mux pattern (or "unmatched") and a clamped
// method.
func TestMetricsLabelsBounded(t *testing.T) {
	srv := New(&fakeStore{}, fakeEmbedder{}, nil)
	h := srv.Handler()
	for i := range 5 {
		req := httptest.NewRequest("GET", fmt.Sprintf("/probe-%d-zzz", i), nil)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("BREW", "/healthz", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/healthz", nil))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	for _, leak := range []string{"probe-0-zzz", "BREW"} {
		if strings.Contains(body, leak) {
			t.Errorf("metrics leak raw request data %q", leak)
		}
	}
	for _, want := range []string{`path="unmatched"`, `path="GET /healthz"`, `method="OTHER"`} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %s", want)
		}
	}
}
