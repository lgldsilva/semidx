package gitmeta

import (
	"net/url"
	"strings"
)

// RedactURL strips userinfo (user:token@ or user@) from a git remote URL so it
// is safe to show in CLI/MCP/admin listings. Non-URL strings (scp-like
// git@host:path) are returned with the userinfo portion dropped; empty input
// is returned unchanged.
func RedactURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return s
	}
	if u, err := url.Parse(s); err == nil && u.Scheme != "" && u.Host != "" {
		u.User = nil
		return u.String()
	}
	// scp-like: user@host:path or user:token@host:path
	if at := strings.Index(s, "@"); at >= 0 {
		rest := s[at+1:]
		if strings.Contains(rest, ":") && !strings.Contains(s[:at], "://") {
			return rest
		}
	}
	return s
}
