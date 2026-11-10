package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Regression: the rate-limit key was the raw Authorization header, so rotating
// random bearer values bypassed the 200 req/s cap and grew the bucket map.
func TestRateLimitKeyedByClientIPNotAuthorization(t *testing.T) {
	srv := New(&fakeStore{}, fakeEmbedder{}, nil)
	h := srv.Handler()
	limited := false
	for i := range 250 {
		req := httptest.NewRequest("GET", "/api/v1/projects", nil)
		req.RemoteAddr = fmt.Sprintf("203.0.113.7:%d", 10000+i) // same host, new port
		req.Header.Set("Authorization", fmt.Sprintf("Bearer random-%d", i))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("rotating bearer tokens bypassed the per-client rate limit")
	}
	srv.apiLimiter.mu.Lock()
	n := len(srv.apiLimiter.counts)
	srv.apiLimiter.mu.Unlock()
	if n != 1 {
		t.Fatalf("limiter holds %d buckets, want 1 (keyed by client IP)", n)
	}
}
