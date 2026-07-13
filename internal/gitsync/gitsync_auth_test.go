package gitsync

import (
	"encoding/base64"
	"slices"
	"strings"
	"testing"
)

func TestNormalizeToHTTPS(t *testing.T) {
	cases := map[string]string{
		"git@github.com:acme/repo.git":                "https://github.com/acme/repo.git",
		"git@gitea.raspberrypi.lan:lgldsilva/x.git":   "https://gitea.raspberrypi.lan/lgldsilva/x.git",
		"ssh://git@192.168.0.100:222/lgldsilva/x.git": "https://192.168.0.100/lgldsilva/x.git",
		"https://github.com/acme/repo.git":            "https://github.com/acme/repo.git", // already https
		"file:///tmp/repo":                            "file:///tmp/repo",                 // untouched
	}
	for in, want := range cases {
		if got := normalizeToHTTPS(in); got != want {
			t.Errorf("normalizeToHTTPS(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGitConfigArgs(t *testing.T) {
	// No auth/cert → no flags.
	if got := gitConfigArgs(Options{}); len(got) != 0 {
		t.Errorf("no options → %v, want empty", got)
	}
	// SSL no-verify.
	if got := gitConfigArgs(Options{SSLNoVerify: true}); !slices.Equal(got, []string{"-c", "http.sslVerify=false"}) {
		t.Errorf("sslNoVerify → %v", got)
	}
	// Token → basic-auth extraheader with the default user; the raw token must
	// NOT appear literally (it's base64-encoded inside user:token).
	got := gitConfigArgs(Options{Token: "ghp_SECRET"})
	if len(got) != 2 || got[0] != "-c" || !strings.HasPrefix(got[1], "http.extraHeader=Authorization: Basic ") {
		t.Fatalf("token → %v", got)
	}
	if strings.Contains(got[1], "ghp_SECRET") {
		t.Errorf("raw token must not appear un-encoded: %q", got[1])
	}
	want := base64.StdEncoding.EncodeToString([]byte("x-access-token:ghp_SECRET"))
	if !strings.HasSuffix(got[1], want) {
		t.Errorf("expected basic auth of x-access-token:token, got %q", got[1])
	}
	// Custom user honored.
	got = gitConfigArgs(Options{Token: "t", TokenUser: "alice"})
	want = base64.StdEncoding.EncodeToString([]byte("alice:t"))
	if !strings.HasSuffix(got[len(got)-1], want) {
		t.Errorf("custom user not honored: %q", got)
	}
}
