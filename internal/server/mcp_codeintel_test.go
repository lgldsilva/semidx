package server

import (
	"context"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

// The code-intelligence tools have no server-side HTTP endpoints yet, so the
// server's MCP backend must answer with the actionable "standalone/local mode
// only" message instead of pretending to run them.
func TestServerBackendCodeIntelIsStandaloneOnly(t *testing.T) {
	b := &serverBackend{s: New(&fakeStore{token: &store.Token{Scopes: []string{"read"}}}, fakeEmbedder{}, nil)}
	ctx := context.Background()

	cases := []struct {
		tool string
		call func() error
	}{
		{"semantic_callers", func() error { _, err := b.Callers(ctx, "p", "Foo", 10); return err }},
		{"semantic_explain", func() error { _, err := b.Explain(ctx, "p", "Foo", 10); return err }},
		{"semantic_impact", func() error { _, err := b.Impact(ctx, "p", "Foo", 2, 10); return err }},
		{"semantic_deadcode", func() error { _, err := b.DeadCode(ctx, "p"); return err }},
		{"semantic_diff", func() error { _, err := b.Diff(ctx, "p"); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatalf("%s: want an error, got nil", tc.tool)
			}
			if !strings.Contains(err.Error(), tc.tool) || !strings.Contains(err.Error(), "standalone/local mode only") {
				t.Errorf("%s: error = %q", tc.tool, err)
			}
		})
	}
}
