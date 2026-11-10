package webadmin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func loginReq(t *testing.T, a *Admin, ip, user, pass string) int {
	t.Helper()
	body := `{"username":"` + user + `","password":"` + pass + `"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/login", strings.NewReader(body))
	req.RemoteAddr = ip + ":40000"
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)
	return rec.Code
}

// Regression: the login limiter was keyed by username only, so anyone could
// lock the admin out by failing its password from anywhere.
func TestLoginLockoutIsPerClientIP(t *testing.T) {
	fs := newFakeStore()
	fs.addUser("admin", "supersecret", "admin")
	a, err := New(fs, fakeEmbedder{}, nil, true, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for range loginMaxTries + 2 {
		loginReq(t, a, "198.51.100.66", "admin", "wrong")
	}
	if code := loginReq(t, a, "198.51.100.66", "admin", "supersecret"); code != http.StatusTooManyRequests {
		t.Fatalf("attacker IP not throttled: %d", code)
	}
	if code := loginReq(t, a, "192.0.2.10", "admin", "supersecret"); code != http.StatusOK {
		t.Fatalf("legit admin locked out from another IP: %d", code)
	}
}

// A single IP spraying many usernames hits a per-IP cap.
func TestLoginPerIPCap(t *testing.T) {
	a, err := New(newFakeStore(), fakeEmbedder{}, nil, true, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	limited := false
	for i := range loginMaxTriesPerIP + 2 {
		if loginReq(t, a, "198.51.100.77", fmt.Sprintf("user%d", i), "x") == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("username spraying from one IP was never throttled")
	}
}

// Regression: an unknown username skipped argon2 entirely, so response time
// revealed which usernames exist. A dummy hash must be verified instead.
func TestLoginUnknownUserStillVerifiesHash(t *testing.T) {
	a, err := New(newFakeStore(), fakeEmbedder{}, nil, true, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	orig := verifyPassword
	verifyPassword = func(pw, enc string) (bool, error) { calls++; return orig(pw, enc) }
	t.Cleanup(func() { verifyPassword = orig })
	if code := loginReq(t, a, "192.0.2.1", "ghost", "pw"); code != http.StatusUnauthorized {
		t.Fatalf("unknown user = %d", code)
	}
	if calls != 1 {
		t.Fatalf("password verification ran %d times for an unknown user, want 1", calls)
	}
}
