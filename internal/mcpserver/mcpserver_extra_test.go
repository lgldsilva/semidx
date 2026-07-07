package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lgldsilva/semidx/pkg/client"
)

// connectErr wires an MCP session to a server whose HTTP API always returns 500,
// so tool handlers hit their error branches.
func connectErr(t *testing.T) *mcp.ClientSession {
	t.Helper()
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	t.Cleanup(httpSrv.Close)

	server := New(NewClientBackend(client.New(httpSrv.URL, "tok")))
	serverT, clientT := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	cli := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	sess, err := cli.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func TestProjectsHandlerError(t *testing.T) {
	sess := connectErr(t)
	text, isErr := callText(t, sess, "semantic_projects", map[string]any{})
	if !isErr {
		t.Errorf("expected in-band error from a failing server; text=%q", text)
	}
	if !strings.Contains(text, "500") && !strings.Contains(text, "boom") {
		t.Errorf("error text should reflect the server failure; got %q", text)
	}
}

func TestReindexHandlerError(t *testing.T) {
	sess := connectErr(t)
	text, isErr := callText(t, sess, "semantic_reindex", map[string]any{"project": "app"})
	if !isErr {
		t.Errorf("expected in-band error from a failing server; text=%q", text)
	}
}

func TestSearchHandlerError(t *testing.T) {
	sess := connectErr(t)
	text, isErr := callText(t, sess, "semantic_search", map[string]any{"project": "app", "query": "x"})
	if !isErr {
		t.Errorf("expected in-band error from a failing server; text=%q", text)
	}
}

func TestFormatSearchText(t *testing.T) {
	t.Run("no results", func(t *testing.T) {
		got := formatSearchText(&SearchOutput{Project: "proj", Results: nil})
		if got != `No results in project "proj" for that query.` {
			t.Errorf("empty formatSearchText = %q", got)
		}
	})

	t.Run("fallback warning is prepended", func(t *testing.T) {
		got := formatSearchText(&SearchOutput{
			Project:  "proj",
			Fallback: true,
			Results:  []Hit{{Path: "a.go", StartLine: 1, Score: 0.5, Content: "x"}},
		})
		if !strings.HasPrefix(got, "[warning] embedding was unavailable") {
			t.Errorf("fallback warning missing; got %q", got)
		}
		if !strings.Contains(got, "a.go:1") {
			t.Errorf("result line missing; got %q", got)
		}
	})

	t.Run("ranked results are numbered with scores", func(t *testing.T) {
		got := formatSearchText(&SearchOutput{
			Project: "proj",
			Results: []Hit{
				{Path: "a.go", StartLine: 10, Score: 0.912, Content: "alpha"},
				{Path: "b.go", StartLine: 20, Score: 0.5, Content: "beta"},
			},
		})
		if !strings.Contains(got, "1. a.go:10  (score 0.912)") {
			t.Errorf("first hit misformatted; got %q", got)
		}
		if !strings.Contains(got, "2. b.go:20  (score 0.500)") {
			t.Errorf("second hit misformatted; got %q", got)
		}
	})

	t.Run("multi-line chunk shows end line", func(t *testing.T) {
		got := formatSearchText(&SearchOutput{
			Project: "proj",
			Results: []Hit{
				{Path: "main.go", StartLine: 10, EndLine: 14, Score: 0.9, Content: "func main() {}"},
			},
		})
		if !strings.Contains(got, "1. main.go:10-14  (score 0.900)") {
			t.Errorf("multi-line result misformatted; got %q", got)
		}
	})
}

func TestFormatSearchStructured(t *testing.T) {
	t.Run("no results", func(t *testing.T) {
		got := formatSearchStructured(&SearchOutput{Project: "proj", TookMS: 5})
		if !strings.Contains(got, `"total_results":0`) || !strings.Contains(got, `"query_time_ms":5`) {
			t.Errorf("empty structured output missing fields; got %q", got)
		}
	})

	t.Run("populated results include all fields", func(t *testing.T) {
		got := formatSearchStructured(&SearchOutput{
			Project:  "proj",
			Fallback: true,
			TookMS:   12,
			Results: []Hit{
				{Path: "auth/token.go", StartLine: 42, EndLine: 48, Score: 0.912, Content: "func Validate()"},
			},
		})
		if !strings.Contains(got, `"file":"auth/token.go"`) {
			t.Errorf("missing file field; got %q", got)
		}
		if !strings.Contains(got, `"start_line":42`) {
			t.Errorf("missing start_line; got %q", got)
		}
		if !strings.Contains(got, `"end_line":48`) {
			t.Errorf("missing end_line; got %q", got)
		}
		if !strings.Contains(got, `"language":"go"`) {
			t.Errorf("missing language; got %q", got)
		}
		if !strings.Contains(got, `"fallback":true`) {
			t.Errorf("missing fallback; got %q", got)
		}
		if !strings.Contains(got, `"total_results":1`) {
			t.Errorf("missing total_results; got %q", got)
		}
		if !strings.Contains(got, `"query_time_ms":12`) {
			t.Errorf("missing query_time_ms; got %q", got)
		}
	})
}

func TestFormatSearchMinimal(t *testing.T) {
	t.Run("no results", func(t *testing.T) {
		got := formatSearchMinimal(&SearchOutput{Project: "proj", TookMS: 3})
		if !strings.Contains(got, `"t":0`) || !strings.Contains(got, `"ms":3`) {
			t.Errorf("empty minimal output missing fields; got %q", got)
		}
	})

	t.Run("populated results use abbreviated keys", func(t *testing.T) {
		got := formatSearchMinimal(&SearchOutput{
			Project:  "proj",
			Fallback: false,
			TookMS:   42,
			Results: []Hit{
				{Path: "auth/token.go", StartLine: 42, EndLine: 48, Score: 0.912, Content: "func Validate() {}"},
			},
		})
		if !strings.Contains(got, `"f":"auth/token.go"`) {
			t.Errorf("missing abbreviated file key; got %q", got)
		}
		if !strings.Contains(got, `"l":"42-48"`) {
			t.Errorf("missing line range; got %q", got)
		}
		if !strings.Contains(got, `"s":0.912`) {
			t.Errorf("missing score; got %q", got)
		}
		if !strings.Contains(got, `"fb":false`) {
			t.Errorf("missing fallback; got %q", got)
		}
		if !strings.Contains(got, `"ms":42`) {
			t.Errorf("missing query_time_ms; got %q", got)
		}
	})

	t.Run("single-line chunk uses single line", func(t *testing.T) {
		got := formatSearchMinimal(&SearchOutput{
			Project: "proj",
			Results: []Hit{
				{Path: "main.go", StartLine: 5, EndLine: 5, Score: 0.5, Content: "single"},
			},
		})
		if !strings.Contains(got, `"l":"5"`) {
			t.Errorf("single line should not have range; got %q", got)
		}
	})
}

func TestFormatProjects(t *testing.T) {
	t.Run("no projects", func(t *testing.T) {
		if got := formatProjects(nil); got != "No projects are registered in this index." {
			t.Errorf("empty formatProjects = %q", got)
		}
	})

	t.Run("git source shows the URL, push source does not", func(t *testing.T) {
		got := formatProjects([]ProjectInfo{
			{Name: "app", SourceType: "git", GitURL: "https://x/y.git", Status: "ready", Model: "bge-m3"},
			{Name: "docs", SourceType: "push", Status: "registered", Model: "bge-m3"},
		})
		if !strings.Contains(got, "- app  [git (https://x/y.git)]  status=ready  model=bge-m3") {
			t.Errorf("git project misformatted; got %q", got)
		}
		if !strings.Contains(got, "- docs  [push]  status=registered  model=bge-m3") {
			t.Errorf("push project misformatted; got %q", got)
		}
	})
}

func TestPreview(t *testing.T) {
	if got := preview("  hello  ", 300); got != "hello" {
		t.Errorf("preview trims and returns short input; got %q", got)
	}
	long := strings.Repeat("x", 50)
	got := preview(long, 10)
	if got != strings.Repeat("x", 10)+"…" {
		t.Errorf("preview should truncate to 10 runes + ellipsis; got %q", got)
	}
}
