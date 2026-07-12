package webadmin

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

// TestLoginRememberMe verifies the "remember me" checkbox extends both the
// server-side session expiry and the session cookie lifetime from sessionTTL
// (24h) to rememberMeTTL (30d), and that the default (unchecked) stays at 24h.
func TestLoginRememberMe(t *testing.T) {
	cases := []struct {
		name    string
		form    url.Values
		wantTTL time.Duration
	}{
		{"default session", url.Values{"username": {"admin"}, "password": {"supersecret"}}, sessionTTL},
		{"remember me", url.Values{"username": {"admin"}, "password": {"supersecret"}, "remember_me": {"1"}}, rememberMeTTL},
	}

	// Cookie Expires is serialised to whole seconds, so allow a small slack.
	const slack = 3 * time.Second

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, fs := newTestAdmin(t)
			fs.addUser("admin", "supersecret", "admin")
			c := newClient(t, srv)

			before := time.Now()
			resp, err := c.PostForm(srv.URL+"/admin/login", tc.form)
			if err != nil {
				t.Fatal(err)
			}
			after := time.Now()
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusSeeOther {
				t.Fatalf("login status = %d; want 303", resp.StatusCode)
			}

			// Server-side session expiry (captured by the fake store).
			if got := fs.lastSessionExpiry; got.Before(before.Add(tc.wantTTL-slack)) || got.After(after.Add(tc.wantTTL+slack)) {
				t.Errorf("session expiry = %v; want ~now+%v", got, tc.wantTTL)
			}

			// Session cookie lifetime.
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
