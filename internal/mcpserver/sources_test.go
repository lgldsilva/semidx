package mcpserver

import (
	"testing"

	"github.com/lgldsilva/semidx/internal/agent"
)

func TestSourcesFromTrace(t *testing.T) {
	t.Run("nil or empty trace returns nil", func(t *testing.T) {
		if out := sourcesFromTrace(nil); len(out) != 0 {
			t.Errorf("sourcesFromTrace(nil) = %d, want 0", len(out))
		}
		if out := sourcesFromTrace([]agent.ToolCallRecord{}); len(out) != 0 {
			t.Errorf("sourcesFromTrace([]) = %d, want 0", len(out))
		}
	})

	t.Run("non-search records filtered out", func(t *testing.T) {
		records := []agent.ToolCallRecord{
			{Tool: "read_file", Result: `{"path": "foo.go"}`},
		}
		if out := sourcesFromTrace(records); len(out) != 0 {
			t.Errorf("expected no sources from non-search tool, got %d", len(out))
		}
	})
}
