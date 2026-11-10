package webadmin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/clientip"
)

func loginViaProxy(t *testing.T, h http.Handler, xff, user, pass string) int {
	t.Helper()
	body := `{"username":"` + user + `","password":"` + pass + `"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/login", strings.NewReader(body))
	req.RemoteAddr = "172.31.250.10:40000"
	req.Header.Set("X-Forwarded-For", xff)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// Regression: behind the staging Caddy every login shared the proxy's IP, so
// one client's failures (per-IP cap) locked everyone out.
func TestLoginLimiterSeparatesClientsBehindTrustedProxy(t *testing.T) {
	fs := newFakeStore()
	fs.addUser("admin", "supersecret", "admin")
	a, err := New(fs, fakeEmbedder{}, nil, true, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	res, err := clientip.New([]string{"172.31.250.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	a.SetClientIPResolver(res)
	h := a.Handler()
	for range loginMaxTries + 1 {
		loginViaProxy(t, h, "198.51.100.66", "admin", "wrong")
	}
	if code := loginViaProxy(t, h, "198.51.100.66", "admin", "supersecret"); code != http.StatusTooManyRequests {
		t.Fatalf("attacker behind proxy not throttled: %d", code)
	}
	if code := loginViaProxy(t, h, "192.0.2.10", "admin", "supersecret"); code != http.StatusOK {
		t.Fatalf("other client behind proxy locked out: %d", code)
	}
}
