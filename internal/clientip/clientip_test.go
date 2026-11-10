package clientip

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func req(remote string, xff ...string) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = remote
	for _, v := range xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	return r
}

func mustResolver(t *testing.T, trusted ...string) *Resolver {
	t.Helper()
	r, err := New(trusted)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestClientIP(t *testing.T) {
	proxy := mustResolver(t, "10.0.0.0/8", "192.0.2.5")
	cases := []struct {
		name string
		res  *Resolver
		in   *http.Request
		want string
	}{
		{"nil resolver uses RemoteAddr", nil, req("203.0.113.9:1234", "1.2.3.4"), "203.0.113.9"},
		{"no trusted proxies ignores XFF", mustResolver(t), req("203.0.113.9:1234", "1.2.3.4"), "203.0.113.9"},
		{"spoofed XFF from untrusted peer ignored", proxy, req("203.0.113.9:1234", "1.2.3.4"), "203.0.113.9"},
		{"trusted proxy single hop", proxy, req("10.1.2.3:5555", "198.51.100.7"), "198.51.100.7"},
		{"never the leftmost: spoof then real client", proxy, req("10.1.2.3:5555", "6.6.6.6, 198.51.100.7"), "198.51.100.7"},
		{"chain of trusted proxies skipped", proxy, req("10.1.2.3:5555", "6.6.6.6, 198.51.100.7, 192.0.2.5, 10.9.9.9"), "198.51.100.7"},
		{"multiple XFF headers concatenated", proxy, req("10.1.2.3:5555", "6.6.6.6", "198.51.100.7, 10.0.0.1"), "198.51.100.7"},
		{"malformed entries ignored", proxy, req("10.1.2.3:5555", "198.51.100.7, garbage, "), "198.51.100.7"},
		{"all trusted falls back to RemoteAddr", proxy, req("10.1.2.3:5555", "10.0.0.2"), "10.1.2.3"},
		{"no XFF falls back to RemoteAddr", proxy, req("10.1.2.3:5555"), "10.1.2.3"},
		{"ipv6 client", proxy, req("10.1.2.3:5555", "2001:db8::1"), "2001:db8::1"},
		{"ipv4-mapped proxy address", proxy, req("[::ffff:10.1.2.3]:5555", "198.51.100.7"), "198.51.100.7"},
		{"RemoteAddr without port", nil, req("203.0.113.9"), "203.0.113.9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.res.ClientIP(c.in); got != c.want {
				t.Errorf("ClientIP = %q, want %q", got, c.want)
			}
		})
	}
}

func TestNewRejectsInvalid(t *testing.T) {
	if _, err := New([]string{"10.0.0.0/8", "not-an-ip"}); err == nil {
		t.Fatal("invalid trusted proxy accepted")
	}
	if _, err := New([]string{" 10.0.0.1 ", "", "fd00::/8"}); err != nil {
		t.Fatalf("valid list rejected: %v", err)
	}
}
