package mcpserver

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

// TestSafeSearchErr locks REQ-SRCH-08 for the standalone (local) MCP backend:
// "not found" stays actionable for the agent, but any other error — which may
// carry DSNs, pgx errors or provider response bodies — is collapsed to a
// generic message so nothing internal reaches the model.
func TestSafeSearchErr(t *testing.T) {
	gotNF := safeSearchErr(fmt.Errorf("resolve project: %w", store.ErrNotFound))
	if !strings.Contains(gotNF.Error(), "project not found") {
		t.Fatalf("not-found mapped to %q, want it to contain %q", gotNF.Error(), "project not found")
	}
	gotNamed := safeSearchErrProject(fmt.Errorf("%w", store.ErrNotFound), "jackui")
	if !strings.Contains(gotNamed.Error(), `"jackui"`) {
		t.Fatalf("named not-found = %q, want project name", gotNamed.Error())
	}

	const leak = "dial tcp 10.0.0.5:5432: connection refused (dsn=postgres://u:pw@db)"
	got := safeSearchErr(errors.New(leak))
	if got.Error() != "search failed" {
		t.Fatalf("infra error mapped to %q, want %q", got.Error(), "search failed")
	}
	if strings.Contains(got.Error(), "5432") || strings.Contains(got.Error(), "dsn=") {
		t.Fatalf("REQ-SRCH-08 leak: sanitized error still exposes internals: %q", got.Error())
	}
}
