package webadmin

import (
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/lgldsilva/semidx/internal/passwd"
)

// loginMaxTriesPerIP caps failed logins from one client IP across all
// usernames (credential spraying), on top of the per IP+username cap.
const loginMaxTriesPerIP = 20

// clientIP is the host part of RemoteAddr. Forwarding headers are ignored:
// without trusted-proxy configuration they are client-controlled.
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// loginKeys returns the limiter keys for an attempt: the IP+username pair
// (so a remote attacker cannot lock a user out everywhere) and the bare IP
// (per-IP spraying cap).
func loginKeys(ip, username string) (pair, ipKey string) {
	return "u|" + ip + "|" + username, "ip|" + ip
}

func (l *loginLimiter) allowedLogin(ip, username string, now time.Time) bool {
	pair, ipKey := loginKeys(ip, username)
	return l.allowedN(pair, loginMaxTries, now) && l.allowedN(ipKey, loginMaxTriesPerIP, now)
}

func (l *loginLimiter) recordLogin(ip, username string, now time.Time) {
	pair, ipKey := loginKeys(ip, username)
	l.record(pair, now)
	l.record(ipKey, now)
}

func (l *loginLimiter) resetLogin(ip, username string) {
	pair, _ := loginKeys(ip, username)
	l.reset(pair)
}

// verifyPassword is swappable in tests to observe that unknown users still pay
// for an argon2 verification.
var verifyPassword = passwd.Verify

// dummyHash is a valid argon2id hash of a random-ish constant, verified when
// the username does not exist so the response time does not reveal it.
var dummyHash = sync.OnceValue(func() string {
	h, err := passwd.Hash("semidx-login-timing-equaliser")
	if err != nil {
		return ""
	}
	return h
})
