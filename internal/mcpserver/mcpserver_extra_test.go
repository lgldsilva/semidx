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

func TestFormatSearch(t *testing.T) {
	t.Run("no results", func(t *testing.T) {
		got := formatSearch(&SearchOutput{Project: "proj", Results: nil})
		if got != `No results in project "proj" for that query.` {
			t.Errorf("empty formatSearch = %q", got)
		}
	})

	t.Run("fallback warning is prepended", func(t *testing.T) {
		got := formatSearch(&SearchOutput{
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
		got := formatSearch(&SearchOutput{
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
