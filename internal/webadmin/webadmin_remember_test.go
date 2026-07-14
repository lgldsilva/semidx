package webadmin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// TestLoginRememberMe verifies remember_me extends both the server-side session
// expiry and the session cookie lifetime from sessionTTL (24h) to rememberMeTTL
// (30d), and that the default (unchecked) stays at 24h.
func TestLoginRememberMe(t *testing.T) {
	cases := []struct {
		name     string
		remember bool
		wantTTL  time.Duration
	}{
		{"default session", false, sessionTTL},
		{"remember me", true, rememberMeTTL},
	}

	const slack = 3 * time.Second

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, fs := newTestAdmin(t)
			fs.addUser("admin", "supersecret", "admin")
			c := newClient(t, srv)

			before := time.Now()
			raw, err := json.Marshal(map[string]any{
				"username": "admin", "password": "supersecret", "remember_me": tc.remember,
			})
			if err != nil {
				t.Fatal(err)
			}
			resp, err := c.Post(srv.URL+"/admin/api/login", "application/json", bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			after := time.Now()
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("login status = %d; want 200", resp.StatusCode)
			}

			if got := fs.lastSessionExpiry; got.Before(before.Add(tc.wantTTL-slack)) || got.After(after.Add(tc.wantTTL+slack)) {
				t.Errorf("session expiry = %v; want ~now+%v", got, tc.wantTTL)
			}

			var ck *http.Cookie
			for _, got := range resp.Cookies() {
				if got.Name == sessionCookie {
					ck = got
				}
			}
			if ck == nil {
				t.Fatal("no session cookie set on login")
			}
			if ck.Expires.Before(before.Add(tc.wantTTL-slack)) || ck.Expires.After(after.Add(tc.wantTTL+slack)) {
				t.Errorf("cookie Expires = %v; want ~now+%v", ck.Expires, tc.wantTTL)
			}
		})
	}
}
