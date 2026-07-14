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
	if got := gitConfigArgs(Options{}, ""); len(got) != 0 {
		t.Errorf("no options → %v, want empty", got)
	}
	// SSL no-verify.
	if got := gitConfigArgs(Options{SSLNoVerify: true}, ""); !slices.Equal(got, []string{"-c", "http.sslVerify=false"}) {
		t.Errorf("sslNoVerify → %v", got)
	}
	// Custom CA file → sslCAInfo.
	want := []string{"-c", "http.sslVerify=false", "-c", "http.sslCAInfo=/data/ssh/tmp/ca.pem"}
	if got := gitConfigArgs(Options{SSLNoVerify: true}, "/data/ssh/tmp/ca.pem"); !slices.Equal(got, want) {
		t.Errorf("caPath → %v, want %v", got, want)
	}
	// The token must NEVER reach argv (it would be visible in `ps`) — it goes
	// through gitConfigEnv instead.
	joined := strings.Join(gitConfigArgs(Options{Token: "ghp_SECRET", SSLNoVerify: true}, ""), " ")
	if strings.Contains(joined, "extraHeader") || strings.Contains(joined, "ghp_SECRET") || strings.Contains(joined, "Basic") {
		t.Errorf("token leaked into argv: %q", joined)
	}
}

func TestGitConfigEnv(t *testing.T) {
	// No token → no env-config injection.
	if got := gitConfigEnv(Options{Token: ""}); got != nil {
		t.Errorf("no token → %v, want nil", got)
	}
	// Token → basic-auth extraheader with the default user; the raw token must
	// NOT appear literally (it's base64-encoded inside user:token).
	got := gitConfigEnv(Options{Token: "ghp_SECRET"})
	wantEnv := []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:ghp_SECRET")),
	}
	if !slices.Equal(got, wantEnv) {
		t.Errorf("token env = %v, want %v", got, wantEnv)
	}
	if strings.Contains(strings.Join(got, " "), "ghp_SECRET") {
		t.Errorf("raw token must not appear un-encoded: %v", got)
	}
	// Custom user honored.
	got = gitConfigEnv(Options{Token: "t", TokenUser: "alice"})
	want := base64.StdEncoding.EncodeToString([]byte("alice:t"))
	if !strings.HasSuffix(got[len(got)-1], want) {
		t.Errorf("custom user not honored: %v", got)
	}
}
