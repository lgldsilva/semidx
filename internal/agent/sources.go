package agent

import "encoding/json"

// SearchHit is one source chunk pulled from a semantic_search tool result in the
// agent trace. It lets callers (admin chat, MCP ask) surface real citations
// instead of discarding the trace.
type SearchHit struct {
	File      string
	StartLine int
	EndLine   int
	Content   string
	Score     float64
	Keyword   bool
	// Project is the source project's label, set only by the global (all-projects)
	// chat so a citation can say which project it came from. Empty otherwise.
	Project string
}

// searchToolResult mirrors the JSON the semantic_search tool returns
// (internal/agent/tools_fantasy.go). Only the fields needed to rebuild citations
// are decoded.
type searchToolResult struct {
	Results []struct {
		File      string  `json:"file"`
		Project   string  `json:"project"`
		StartLine int     `json:"start_line"`
		EndLine   int     `json:"end_line"`
		Content   string  `json:"content"`
		Score     float64 `json:"score"`
	} `json:"results"`
	Keyword bool `json:"keyword"`
}

// SourcesFromTrace extracts deduped search hits from the semantic_search results
// captured in the agent trace, keeping the highest score per file+line span. It
// also reports whether any search fell back to keyword ranking, so callers can
// set an accurate fallback flag instead of guessing.
func SourcesFromTrace(trace []ToolCallRecord) (hits []SearchHit, fallback bool) {
	type spanKey struct {
		path       string
		start, end int
	}
	seen := make(map[spanKey]int)
	hits = []SearchHit{}
	for _, tc := range trace {
		if tc.Tool != "semantic_search" || tc.Error != "" || tc.Result == "" {
			continue
		}
		var r searchToolResult
		if err := json.Unmarshal([]byte(tc.Result), &r); err != nil {
			continue
		}
		if r.Keyword {
			fallback = true
		}
		for _, h := range r.Results {
			k := spanKey{h.File, h.StartLine, h.EndLine}
			if idx, ok := seen[k]; ok {
				if h.Score > hits[idx].Score {
					hits[idx].Score = h.Score
				}
				continue
			}
			seen[k] = len(hits)
			hits = append(hits, SearchHit{
				File: h.File, StartLine: h.StartLine, EndLine: h.EndLine,
				Content: h.Content, Score: h.Score, Keyword: r.Keyword,
				Project: h.Project,
			})
		}
	}
	return hits, fallback
}
