package webadmin

import (
	"errors"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func TestJobToJSONSanitizesFailedError(t *testing.T) {
	got := jobToJSON(&store.Job{
		ID:     42,
		Status: "failed",
		Error:  "pq: password authentication failed for user semidx",
	})
	if got["error"] != "index job failed" {
		t.Fatalf("error = %v, want generic failed message", got["error"])
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
