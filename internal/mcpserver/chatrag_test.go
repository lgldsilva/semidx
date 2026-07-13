package mcpserver

import (
	"testing"

	"github.com/lgldsilva/semidx/internal/agent"
)

func TestSourcesFromTrace_parsesRealHits(t *testing.T) {
	trace := []agent.ToolCallRecord{
		{
			Tool:   "semantic_search",
			Result: `{"results":[{"file":"a.go","start_line":1,"end_line":10,"score":0.9},{"file":"b.go","start_line":5,"end_line":8,"score":0.4}],"keyword":false}`,
		},
	}
	got := sourcesFromTrace(trace)
	if len(got) != 2 {
		t.Fatalf("want 2 sources, got %d: %+v", len(got), got)
	}
	if got[0].Path != "a.go" || got[0].StartLine != 1 || got[0].EndLine != 10 || got[0].Score != 0.9 {
		t.Errorf("source[0] = %+v", got[0])
	}
	if got[0].Keyword {
		t.Errorf("keyword flag should be false")
	}
}

func TestSourcesFromTrace_keywordFlag(t *testing.T) {
	trace := []agent.ToolCallRecord{
		{Tool: "semantic_search", Result: `{"results":[{"file":"a.go","start_line":1,"end_line":2,"score":0}],"keyword":true}`},
	}
	got := sourcesFromTrace(trace)
	if len(got) != 1 || !got[0].Keyword {
		t.Errorf("keyword source not propagated: %+v", got)
	}
}

func TestSourcesFromTrace_dedupeKeepsHighestScore(t *testing.T) {
	// Two searches return the same span with different scores.
	trace := []agent.ToolCallRecord{
		{Tool: "semantic_search", Result: `{"results":[{"file":"a.go","start_line":1,"end_line":10,"score":0.3}]}`},
		{Tool: "semantic_search", Result: `{"results":[{"file":"a.go","start_line":1,"end_line":10,"score":0.8}]}`},
	}
	got := sourcesFromTrace(trace)
	if len(got) != 1 {
		t.Fatalf("duplicate span should collapse to 1, got %d", len(got))
	}
	if got[0].Score != 0.8 {
		t.Errorf("dedupe should keep the highest score, got %v", got[0].Score)
	}
}

func TestSourcesFromTrace_ignoresNonSearchAndErrors(t *testing.T) {
	trace := []agent.ToolCallRecord{
		{Tool: "list_projects", Result: `{"projects":[]}`},                 // not a search
		{Tool: "semantic_search", Error: "boom", Result: `{"results":[]}`}, // errored
		{Tool: "semantic_search", Result: ""},                              // empty
		{Tool: "semantic_search", Result: "not json"},                      // malformed
	}
	got := sourcesFromTrace(trace)
	if len(got) != 0 {
		t.Errorf("want no sources, got %d: %+v", len(got), got)
	}
	// Never nil — callers expect an empty slice, not nil.
	if got == nil {
		t.Error("sourcesFromTrace must return a non-nil slice")
	}
}
