package webadmin

import (
	"errors"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func TestJobToJSONFailedErrorIsSafeSummary(t *testing.T) {
	// A DB/infra error (may reveal DSN/host/user) maps to the generic summary —
	// no raw detail leaks.
	got := jobToJSON(&store.Job{
		ID:     42,
		Status: "failed",
		Error:  "pq: password authentication failed for user semidx",
	})
	msg, _ := got["error"].(string)
	if !strings.Contains(msg, "index job failed") {
		t.Fatalf("db error should map to the generic summary, got %q", msg)
	}
	if strings.Contains(msg, "semidx") || strings.Contains(msg, "password authentication") {
		t.Fatalf("raw db error leaked via API: %q", msg)
	}

	// A git-clone-via-ssh failure maps to an actionable summary (use https) with
	// no raw path/host leaked — this is what tells the admin why a reindex "did
	// nothing".
	got = jobToJSON(&store.Job{
		ID:     43,
		Status: "failed",
		Error:  "git clone: exit status 128: Cloning into '/var/lib/semidx/repos/x'...\nerror: cannot run ssh: No such file or directory\nfatal: unable to fork",
	})
	msg, _ = got["error"].(string)
	if !strings.Contains(msg, "https://") {
		t.Errorf("ssh clone failure should suggest an https URL, got %q", msg)
	}
	if strings.Contains(msg, "/var/lib/semidx") {
		t.Errorf("raw clone path leaked via API: %q", msg)
	}
}

func TestJobToJSONKeepsNonFailedErrorAsIs(t *testing.T) {
	got := jobToJSON(&store.Job{
		ID:     7,
		Status: "running",
		Error:  "transient",
	})
	if got["error"] != "transient" {
		t.Fatalf("error = %v, want original message for non-failed jobs", got["error"])
	}
}

func TestSanitizeIngestIndexError(t *testing.T) {
	got := sanitizeIngestIndexError(errors.New("dial tcp 10.0.0.5:5432: connect: refused"))
	if got != "indexing failed for this file" {
		t.Fatalf("sanitizeIngestIndexError returned %q", got)
	}
}
