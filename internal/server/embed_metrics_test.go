package server

import (
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

// TestEmbedMetricsRecordedOnSearch verifies REQ-OPS-03: a search embeds the
// query through the instrumented embedder, so the /metrics exposition includes
// the embed input counter and duration histogram labelled by the project model.
func TestEmbedMetricsRecordedOnSearch(t *testing.T) {
	srv := New(&fakeStore{
		token:   &store.Token{Scopes: []string{"read"}},
		project: &store.Project{ID: 1, Name: "proj", Model: "bge-m3"},
		results: []store.SearchResult{{FilePath: "a.go", Score: 0.9, Content: "x"}},
	}, fakeEmbedder{}, nil)

	if rec := do(t, srv, "POST", "/api/v1/projects/proj/search", "tok", `{"query":"auth"}`); rec.Code != 200 {
		t.Fatalf("search = %d: %s", rec.Code, rec.Body.String())
	}

	body := do(t, srv, "GET", "/metrics", "", "").Body.String()
	if !strings.Contains(body, `semidx_embed_inputs_total{model="bge-m3"}`) {
		t.Errorf("/metrics missing embed input counter for bge-m3:\n%s", excerpt(body, "semidx_embed"))
	}
	if !strings.Contains(body, "semidx_embed_duration_seconds_count") {
		t.Errorf("/metrics missing embed duration histogram:\n%s", excerpt(body, "semidx_embed"))
	}
}

// excerpt returns the lines of s containing sub, for a focused failure message.
func excerpt(s, sub string) string {
	var b strings.Builder
	for _, ln := range strings.Split(s, "\n") {
		if strings.Contains(ln, sub) {
			b.WriteString(ln)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
