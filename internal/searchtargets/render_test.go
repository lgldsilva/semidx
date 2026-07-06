package searchtargets

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
)

func TestRenderSearchJSONSingleProject(t *testing.T) {
	var buf bytes.Buffer
	err := RenderSearchJSON(&buf, []NamedResult{{
		Name: "app",
		Resp: &search.Response{
			Model: "bge-m3",
			Results: []store.SearchResult{{
				FilePath: "main.go", Content: "hit", Score: 0.9,
			}},
		},
	}}, nil)
	if err != nil || !strings.Contains(buf.String(), `"file": "main.go"`) {
		t.Fatalf("single = %q, %v", buf.String(), err)
	}
}

func TestRenderSearchJSONMultiProject(t *testing.T) {
	var buf bytes.Buffer
	err := RenderSearchJSON(&buf, []NamedResult{
		{Name: "a", Resp: &search.Response{Model: "m", Results: []store.SearchResult{{FilePath: "a.go", Score: 0.8}}}},
		{Name: "b", Resp: &search.Response{Model: "m", Fallback: true, Results: []store.SearchResult{{FilePath: "b.go", Score: 0.7}}}},
	}, []time.Duration{time.Millisecond, 2 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	if !strings.Contains(body, `"projects"`) || !strings.Contains(body, `"project": "a"`) || !strings.Contains(body, `"fallback": true`) {
		t.Fatalf("multi = %s", body)
	}
}
