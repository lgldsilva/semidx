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
	_ = took
	if len(results) == 1 {
		return search.JSONFormatter{}.Format(w, results[0].Resp)
	}
	type row struct {
		File    string  `json:"file"`
		Score   float64 `json:"score"`
		Content string  `json:"content"`
	}
	type proj struct {
		Project      string `json:"project"`
		Model        string `json:"model"`
		Fallback     bool   `json:"fallback"`
		Degraded     bool   `json:"degraded"`
		RetryAfterMS int64  `json:"retry_after_ms"`
		Results      []row  `json:"results"`
	}
	out := struct {
		Projects []proj `json:"projects"`
	}{Projects: []proj{}}
	for _, ps := range results {
		p := proj{
			Project: ps.Name, Model: ps.Resp.Model, Fallback: ps.Resp.Fallback,
			Degraded: ps.Resp.Degraded, RetryAfterMS: ps.Resp.RetryAfter.Milliseconds(),
			Results: []row{},
		}
		for _, r := range ps.Resp.Results {
			p.Results = append(p.Results, row{File: r.FilePath, Score: r.Score, Content: r.Content})
		}
		out.Projects = append(out.Projects, p)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
