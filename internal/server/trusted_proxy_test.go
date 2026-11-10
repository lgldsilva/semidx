package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lgldsilva/semidx/internal/clientip"
)

func hitUntilLimited(h http.Handler, remote, xff string, n int) bool {
	for i := range n {
		req := httptest.NewRequest("GET", "/api/v1/projects", nil)
		req.RemoteAddr = fmt.Sprintf("%s:%d", remote, 10000+i)
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			return true
		}
	}
	return false
}

// Regression: behind a reverse proxy every client shared the proxy's bucket.
// With SEMIDX_TRUSTED_PROXIES the client comes from X-Forwarded-For.
func TestRateLimitSeparateBucketsBehindTrustedProxy(t *testing.T) {
	res, err := clientip.New([]string{"172.31.250.10"})
	if err != nil {
		t.Fatal(err)
	}
	srv := New(&fakeStore{}, fakeEmbedder{}, nil)
	srv.SetClientIPResolver(res)
	h := srv.Handler()
	if !hitUntilLimited(h, "172.31.250.10", "198.51.100.1", 250) {
		t.Fatal("client A never limited")
	}
	if hitUntilLimited(h, "172.31.250.10", "198.51.100.2", 5) {
		t.Fatal("client B shares client A's bucket behind the proxy")
	}
}

// A spoofed X-Forwarded-For from an untrusted peer must not create buckets.
func TestRateLimitIgnoresSpoofedXFFFromUntrustedPeer(t *testing.T) {
	res, err := clientip.New([]string{"172.31.250.10"})
	if err != nil {
		t.Fatal(err)
	}
	srv := New(&fakeStore{}, fakeEmbedder{}, nil)
	srv.SetClientIPResolver(res)
	h := srv.Handler()
	limited := false
	for i := range 250 {
		req := httptest.NewRequest("GET", "/api/v1/projects", nil)
		req.RemoteAddr = "203.0.113.7:4000"
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i%250))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("spoofed X-Forwarded-For from an untrusted peer bypassed the limit")
	}
}
