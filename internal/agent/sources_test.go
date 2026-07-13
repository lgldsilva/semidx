package agent

import "testing"

func TestSourcesFromTrace(t *testing.T) {
	trace := []ToolCallRecord{
		{Tool: "list_projects", Result: `{"projects":[]}`},                 // ignored (not search)
		{Tool: "semantic_search", Error: "boom", Result: `{"results":[]}`}, // ignored (errored)
		{Tool: "semantic_search", Result: `not json`},                      // ignored (malformed)
		{Tool: "semantic_search", Result: `{"results":[{"file":"a.go","start_line":1,"end_line":10,"content":"x","score":0.3}],"keyword":false}`},
		// duplicate span, higher score + keyword fallback
		{Tool: "semantic_search", Result: `{"results":[{"file":"a.go","start_line":1,"end_line":10,"content":"x","score":0.8},{"file":"b.go","start_line":2,"end_line":4,"content":"y","score":0.5}],"keyword":true}`},
	}
	hits, fallback := SourcesFromTrace(trace)
	if len(hits) != 2 {
		t.Fatalf("want 2 deduped hits, got %d: %+v", len(hits), hits)
	}
	if hits[0].File != "a.go" || hits[0].Score != 0.8 {
		t.Errorf("dedup should keep highest score for a.go, got %+v", hits[0])
	}
	if hits[0].Content != "x" {
		t.Errorf("content should be captured, got %q", hits[0].Content)
	}
	if !fallback {
		t.Error("fallback should be true when any search returned keyword=true")
	}

	// No search hits → empty (non-nil) slice, fallback false.
	hits, fallback = SourcesFromTrace(nil)
	if hits == nil || len(hits) != 0 || fallback {
		t.Errorf("empty trace: hits=%v fallback=%v", hits, fallback)
	}
}
