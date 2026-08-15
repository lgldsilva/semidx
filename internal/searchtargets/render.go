package searchtargets

import (
	"encoding/json"
	"io"
	"time"

	"github.com/lgldsilva/semidx/internal/search"
)

// RenderSearchJSON emits one project's results via the standard JSONFormatter, or
// a {"projects":[…]} array when several projects were searched.
func RenderSearchJSON(w io.Writer, results []NamedResult, took []time.Duration) error {
	if len(results) == 1 {
		if results[0].Resp.TookMS == 0 && len(took) == 1 {
			results[0].Resp.TookMS = took[0].Milliseconds()
		}
		return search.JSONFormatter{}.Format(w, results[0].Resp)
	}
	type row struct {
		File       string  `json:"file"`
		StartLine  int     `json:"start_line"`
		EndLine    int     `json:"end_line"`
		Score      float64 `json:"score"`
		Content    string  `json:"content"`
		Confidence string  `json:"confidence,omitempty"`
		Symbol     string  `json:"symbol,omitempty"`
		Source     string  `json:"source,omitempty"`
		GraphDepth int     `json:"graph_depth,omitempty"`
		Stale      bool    `json:"stale,omitempty"`
		IndexedAt  string  `json:"indexed_at,omitempty"`
	}
	type proj struct {
		Project      string `json:"project"`
		Model        string `json:"model"`
		Route        string `json:"route,omitempty"`
		Fallback     bool   `json:"fallback"`
		Keyword      bool   `json:"keyword"`
		Degraded     bool   `json:"degraded"`
		RetryAfterMS int64  `json:"retry_after_ms"`
		TookMS       int64  `json:"took_ms"`
		Results      []row  `json:"results"`
	}
	out := struct {
		Projects []proj `json:"projects"`
	}{Projects: []proj{}}
	for i, ps := range results {
		p := proj{
			Project: ps.Name, Model: ps.Resp.Model, Route: ps.Resp.Route,
			Fallback: ps.Resp.Fallback, Keyword: ps.Resp.Keyword,
			Degraded: ps.Resp.Degraded, RetryAfterMS: ps.Resp.RetryAfter.Milliseconds(),
			TookMS:  ps.Resp.TookMS,
			Results: []row{},
		}
		if p.TookMS == 0 && i < len(took) {
			p.TookMS = took[i].Milliseconds()
		}
		for _, r := range ps.Resp.Results {
			indexedAt := ""
			if !r.IndexedAt.IsZero() {
				indexedAt = r.IndexedAt.UTC().Format(time.RFC3339)
			}
			p.Results = append(p.Results, row{
				File: r.FilePath, StartLine: r.StartLine, EndLine: r.EndLine,
				Score: r.Score, Content: r.Content, Confidence: r.Confidence,
				Symbol: r.Symbol, Source: r.Source, GraphDepth: r.GraphDepth,
				Stale: r.Stale, IndexedAt: indexedAt,
			})
		}
		out.Projects = append(out.Projects, p)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
