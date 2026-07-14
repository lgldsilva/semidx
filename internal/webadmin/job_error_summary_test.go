package webadmin

import (
	"strings"
	"testing"
)

// TestJobErrorSummary pins the curated summaries: raw job errors (which may
// carry hosts, DSNs or credentials) must map to safe, actionable messages —
// including the SSH host-key and publickey failures produced by key-based
// server-side clones.
func TestJobErrorSummary(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want string // substring of the curated summary
	}{
		{
			name: "ssh host key verification failed",
			raw:  "git clone: exit status 128: Host key verification failed.\nfatal: Could not read from remote repository.",
			want: "SSH host key verification failed",
		},
		{
			name: "ssh publickey permission denied",
			raw:  "git pull: exit status 128: git@gitea.lan: Permission denied (publickey).\nfatal: Could not read from remote repository.",
			want: "permission denied, publickey",
		},
		{
			name: "stored credential decrypt failure",
			raw:  "git credential 3 could not be decrypted (was SEMIDX_SECRET_KEY changed?) — re-save the credential",
			want: "stored git credential could not be used",
		},
		{
			name: "stored credential lookup failure",
			raw:  "git credential lookup failed — see server logs",
			want: "stored git credential could not be used",
		},
		{
			name: "no ssh client",
			raw:  "git clone: exit status 128: error: cannot run ssh: No such file or directory",
			want: "no SSH client",
		},
		{
			name: "tls certificate",
			raw:  "git clone: exit status 128: fatal: unable to access 'https://gitea.lan/x.git/': SSL certificate problem",
			want: "TLS certificate not trusted",
		},
		{
			name: "https auth failure",
			raw:  "git clone: exit status 128: fatal: Authentication failed for 'https://gitea.lan/x.git/'",
			want: "authentication required or invalid",
		},
		{
			name: "repo not found",
			raw:  "git clone: exit status 128: fatal: repository 'https://gitea.lan/x.git/' not found",
			want: "repository not found or no access",
		},
		{
			name: "generic git failure",
			raw:  "git pull: exit status 1: fatal: unexpected breakage",
			want: "git clone/pull failed — see server logs",
		},
		{
			name: "db auth error is not classified as git",
			raw:  "pq: password authentication failed for user \"semidx\"",
			want: "index job failed — see server logs",
		},
		{
			name: "embedding failure",
			raw:  "model info: embedding provider unreachable",
			want: "embedding/store error",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := jobErrorSummary(tc.raw)
			if !strings.Contains(got, tc.want) {
				t.Errorf("jobErrorSummary(%q) = %q, want substring %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestJobErrorSummaryNeverEchoesRawSecrets asserts the summary is always one
// of the curated strings — no fragment of the raw error (which could contain
// a token or DSN) may leak through.
func TestJobErrorSummaryNeverEchoesRawSecrets(t *testing.T) {
	t.Parallel()
	raws := []string{
		"git clone: fatal: could not read Username for 'https://x-access-token:sekret-token@gitea.lan'",
		"git credential 9 could not be decrypted: cipher: message authentication failed (blob sekret-blob)",
		"git pull: git@gitea.lan: Permission denied (publickey). key=/data/ssh/tmp/key-123",
	}
	for _, raw := range raws {
		if got := jobErrorSummary(raw); strings.Contains(got, "sekret") || strings.Contains(got, "/data/ssh/tmp") {
			t.Errorf("jobErrorSummary(%q) = %q leaks raw error content", raw, got)
		}
	}
}
