package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lgldsilva/semidx/pkg/client"
)

// --- Formatting function unit tests ---

func TestFormatStatus(t *testing.T) {
	t.Parallel()

	// Full info.
	info := &StatusInfo{
		Name: "my-project", SourceType: "git", Identity: "git:r1",
		Status: "ready", Model: "bge-m3", TotalFiles: 42,
	}
	got := formatStatus(info)
	if !strings.Contains(got, "my-project") {
		t.Errorf("formatStatus missing name: %q", got)
	}
	if !strings.Contains(got, "Identity: git:r1") {
		t.Errorf("formatStatus missing identity: %q", got)
	}
	if !strings.Contains(got, "Source: git") {
		t.Errorf("formatStatus missing source: %q", got)
	}
	if !strings.Contains(got, "Status: ready") {
		t.Errorf("formatStatus missing status: %q", got)
	}
	if !strings.Contains(got, "Model: bge-m3") {
		t.Errorf("formatStatus missing model: %q", got)
	}
	if !strings.Contains(got, "Total indexed: 42 files") {
		t.Errorf("formatStatus missing file count: %q", got)
	}
	if !strings.Contains(got, "Tip:") {
		t.Errorf("formatStatus missing tip: %q", got)
	}

	// Minimal info (no identity, no model).
	minimal := &StatusInfo{
		Name: "minimal", SourceType: "path", Status: "indexing", TotalFiles: 0,
	}
	minGot := formatStatus(minimal)
	if strings.Contains(minGot, "Identity:") {
		t.Errorf("minimal status should omit identity: %q", minGot)
	}
	if strings.Contains(minGot, "Model:") {
		t.Errorf("minimal status should omit model: %q", minGot)
	}
	if !strings.Contains(minGot, "Total indexed: 0 files") {
		t.Errorf("minimal status missing file count: %q", minGot)
	}
}

func TestFormatSearch(t *testing.T) {
	t.Parallel()

	// Empty results.
	empty := formatSearchText(&SearchOutput{Project: "empty-proj", Results: nil})
	if !strings.Contains(empty, "No results in project \"empty-proj\"") {
		t.Errorf("empty formatSearchText = %q", empty)
	}

	// With results, no fallback.
	out := &SearchOutput{
		Project:  "proj",
		Fallback: false,
		Results: []Hit{
			{Path: "main.go", StartLine: 10, Score: 0.95, Content: "func main() {}"},
		},
	}
	got := formatSearchText(out)
	if !strings.Contains(got, "1. main.go:10") {
		t.Errorf("formatSearchText missing result line: %q", got)
	}
	if !strings.Contains(got, "0.950") {
		t.Errorf("formatSearchText missing score: %q", got)
	}
	if !strings.Contains(got, "func main() {}") {
		t.Errorf("formatSearchText missing content: %q", got)
	}
	if strings.Contains(got, "warning") {
		t.Errorf("formatSearchText should not contain warning without fallback: %q", got)
	}

	// With fallback warning.
	fallbackOut := &SearchOutput{
		Project:  "proj",
		Fallback: true,
		Results: []Hit{
			{Path: "a.go", StartLine: 1, Score: 0.5, Content: "package a"},
		},
	}
	fbGot := formatSearchText(fallbackOut)
	if !strings.Contains(fbGot, "warning") || !strings.Contains(fbGot, "keyword") {
		t.Errorf("formatSearchText fallback missing warning: %q", fbGot)
	}
}

func TestFormatSearchStructuredAndMinimal(t *testing.T) {
	t.Parallel()
	empty := formatSearchStructured(&SearchOutput{Project: "p"})
	if !strings.Contains(empty, `"total_results":0`) {
		t.Fatalf("structured empty=%s", empty)
	}
	out := &SearchOutput{
		Project: "demo",
		Results: []Hit{{Path: "main.go", StartLine: 3, EndLine: 5, Score: 0.8, Content: "func main() {}"}},
		TookMS:  12,
	}
	structured := formatSearchStructured(out)
	if !strings.Contains(structured, `"language":"go"`) || !strings.Contains(structured, `"file":"main.go"`) {
		t.Fatalf("structured=%s", structured)
	}
	minimal := formatSearchMinimal(out)
	if !strings.Contains(minimal, `"f":"main.go"`) || !strings.Contains(minimal, `"l":"3-5"`) {
		t.Fatalf("minimal=%s", minimal)
	}
}

func TestDetectLanguage(t *testing.T) {
	t.Parallel()
	if got := detectLanguage("x.go"); got != "go" {
		t.Fatalf("go=%q", got)
	}
	if got := detectLanguage("app.tsx"); got != "tsx" {
		t.Fatalf("tsx=%q", got)
	}
	if got := detectLanguage("README"); got != "" {
		t.Fatalf("unknown=%q", got)
	}
}

func TestSearchHandlerFormats(t *testing.T) {
	t.Parallel()
	b := &stubBackend{
		searchFunc: func(_ context.Context, project, query, model string, topK int, graph bool, graphDepth int) (*SearchOutput, error) {
			return &SearchOutput{
				Project: project,
				Results: []Hit{{Path: "a.go", StartLine: 1, EndLine: 2, Score: 0.9, Content: "code"}},
			}, nil
		},
	}
	server := New(b)
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

	for _, format := range []string{"structured", "minimal"} {
		text, isErr := callText(t, sess, "semantic_search", map[string]any{
			"project": "app", "query": "x", "format": format,
		})
		if isErr || !strings.Contains(text, `"`) {
			t.Fatalf("format=%s isErr=%v text=%q", format, isErr, text)
		}
	}
}

func TestFormatProjects(t *testing.T) {
	t.Parallel()

	// Empty list.
	if got := formatProjects(nil); got != "No projects are registered in this index." {
		t.Errorf("empty projects = %q", got)
	}

	// Single project without git URL.
	projs := []ProjectInfo{
		{Name: "local-proj", SourceType: "path", Status: "ready", Model: "bge-m3"},
	}
	got := formatProjects(projs)
	if !strings.Contains(got, "local-proj") || !strings.Contains(got, "status=ready") {
		t.Errorf("formatProjects = %q", got)
	}

	// Project with git URL.
	projsWithGit := []ProjectInfo{
		{Name: "git-proj", SourceType: "git", GitURL: "https://example.com/r.git", Status: "indexing", Model: "bge-m3"},
	}
	gitGot := formatProjects(projsWithGit)
	if !strings.Contains(gitGot, "git (https://example.com/r.git)") {
		t.Errorf("formatProjects missing git URL: %q", gitGot)
	}
}

func TestPreview(t *testing.T) {
	t.Parallel()

	// Short text is returned as-is.
	short := "hello world"
	if got := preview(short, 100); got != short {
		t.Errorf("preview(short) = %q, want %q", got, short)
	}

	// Long text is truncated with ellipsis.
	long := "this is a very long string that should definitely be truncated by the preview function"
	got := preview(long, 20)
	if len(got) != 23 { // 20 chars + "…" (3 bytes in UTF-8)
		t.Errorf("preview truncation len = %d, want 23 (20 + …)", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("preview should end with …: %q", got)
	}

	// Whitespace-trimmed.
	spaced := "  padded  "
	if got := preview(spaced, 100); got != "padded" {
		t.Errorf("preview(trimmed) = %q, want trimmed", got)
	}
}

func TestErrorResult(t *testing.T) {
	t.Parallel()
	err := errors.New("test error")
	res := errorResult(err)
	if !res.IsError {
		t.Error("errorResult.IsError = false, want true")
	}
	if len(res.Content) == 0 {
		t.Fatal("errorResult has no content")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok || tc.Text != "test error" {
		t.Errorf("errorResult content = %q, want test error", tc.Text)
	}
}

func TestTextResult(t *testing.T) {
	t.Parallel()
	res := textResult("hello")
	if res.IsError {
		t.Error("textResult.IsError = true, want false")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok || tc.Text != "hello" {
		t.Errorf("textResult content = %q, want hello", tc.Text)
	}
}

// --- stubs for backend testing ---

// stubBackend implements Backend for unit testing handlers.
type stubBackend struct {
	searchFunc   func(ctx context.Context, project, query, model string, topK int, graph bool, graphDepth int) (*SearchOutput, error)
	projectsFunc func(ctx context.Context) ([]ProjectInfo, error)
	reindexFunc  func(ctx context.Context, project, jobType string) (string, error)
	statusFunc   func(ctx context.Context, project string) (*StatusInfo, error)
}

func (b *stubBackend) Search(ctx context.Context, project, query, model string, topK int, graph bool, graphDepth int) (*SearchOutput, error) {
	return b.searchFunc(ctx, project, query, model, topK, graph, graphDepth)
}
func (b *stubBackend) Projects(ctx context.Context) ([]ProjectInfo, error) {
	return b.projectsFunc(ctx)
}
func (b *stubBackend) Reindex(ctx context.Context, project, jobType string) (string, error) {
	return b.reindexFunc(ctx, project, jobType)
}
func (b *stubBackend) Status(ctx context.Context, project string) (*StatusInfo, error) {
	return b.statusFunc(ctx, project)
}

func TestStatusHandler(t *testing.T) {
	t.Parallel()

	b := &stubBackend{
		statusFunc: func(_ context.Context, project string) (*StatusInfo, error) {
			if project == "ghost" {
				return nil, errors.New("project not found: ghost")
			}
			return &StatusInfo{Name: project, SourceType: "git", Status: "ready", TotalFiles: 10}, nil
		},
	}

	server := New(b)
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

	// Existing project.
	text, isErr := callText(t, sess, "semantic_status", map[string]any{"project": "app"})
	if isErr {
		t.Fatalf("unexpected isError; text=%q", text)
	}
	if !strings.Contains(text, "app") || !strings.Contains(text, "Total indexed: 10 files") {
		t.Errorf("status text = %q", text)
	}

	// Missing project.
	errText, isErr := callText(t, sess, "semantic_status", map[string]any{"project": "ghost"})
	if !isErr {
		t.Errorf("expected isError for missing project; text=%q", errText)
	}
	if !strings.Contains(errText, "not found") {
		t.Errorf("error text = %q, want 'not found'", errText)
	}
}

func TestSearchHandlerEdgeCases(t *testing.T) {
	t.Parallel()

	b := &stubBackend{
		searchFunc: func(_ context.Context, project, query, model string, topK int, graph bool, graphDepth int) (*SearchOutput, error) {
			if project == "err" {
				return nil, errors.New("search failed")
			}
			return &SearchOutput{Project: project, Results: []Hit{{Path: "f.go", StartLine: 1, Score: 0.9, Content: "code"}}}, nil
		},
	}

	server := New(b)
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

	// Graph=true with default depth (text format to match assertion).
	text, isErr := callText(t, sess, "semantic_search", map[string]any{"project": "app", "query": "test", "graph": true, "format": "text"})
	if isErr {
		t.Fatalf("unexpected isError; text=%q", text)
	}
	if !strings.Contains(text, "f.go:1") {
		t.Errorf("search with graph text = %q", text)
	}

	// Backend error is surfaced as in-band error.
	errText, isErr := callText(t, sess, "semantic_search", map[string]any{"project": "err", "query": "x"})
	if !isErr {
		t.Errorf("expected isError for backend error; text=%q", errText)
	}
	if !strings.Contains(errText, "search failed") {
		t.Errorf("error text = %q", errText)
	}
}

func TestReindexHandlerEdgeCases(t *testing.T) {
	t.Parallel()

	b := &stubBackend{
		reindexFunc: func(_ context.Context, project, jobType string) (string, error) {
			if project == "fail" {
				return "", errors.New("reindex failed")
			}
			return "queued full re-index for project", nil
		},
	}

	server := New(b)
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

	// Default job type is "full".
	text, isErr := callText(t, sess, "semantic_reindex", map[string]any{"project": "app"})
	if isErr {
		t.Fatalf("unexpected isError; text=%q", text)
	}
	if !strings.Contains(text, "queued") {
		t.Errorf("reindex text = %q", text)
	}

	// Custom job type.
	text2, isErr := callText(t, sess, "semantic_reindex", map[string]any{"project": "app", "type": "git_history"})
	if isErr {
		t.Fatalf("unexpected isError; text=%q", text2)
	}
	if !strings.Contains(text2, "queued") {
		t.Errorf("reindex with type text = %q", text2)
	}

	// Backend error.
	errText, isErr := callText(t, sess, "semantic_reindex", map[string]any{"project": "fail"})
	if !isErr {
		t.Errorf("expected isError; text=%q", errText)
	}
	if !strings.Contains(errText, "failed") {
		t.Errorf("error text = %q", errText)
	}
}

// TestClientBackendStatus tests the clientBackend.Status path through the
// HTTP API stub (it was previously uncovered).
func TestClientBackendStatus(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{project}/status", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("project") == "ghost" {
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": r.PathValue("project"), "source_type": "git", "identity": "id:r1",
			"status": "ready", "model": "bge-m3", "total_files": 42,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := NewClientBackend(client.New(srv.URL, "tok"))

	// Known project.
	info, err := client.Status(context.Background(), "app")
	if err != nil {
		t.Fatalf("client.Status: %v", err)
	}
	if info.Name != "app" || info.TotalFiles != 42 || info.Model != "bge-m3" {
		t.Errorf("status info = %+v", info)
	}

	// Unknown project -> error.
	_, err = client.Status(context.Background(), "ghost")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("client.Status(ghost) err = %v, want 'not found'", err)
	}
}

// TestClientBackendSearchAndProjects tests the previously uncovered
// clientBackend.Projects and clientBackend.Search paths.
func TestClientBackendSearchAndProjects(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"projects": []map[string]any{
			{"name": "app", "source_type": "git", "git_url": "https://x/y.git", "status": "ready", "model": "bge-m3"},
		}})
	})
	mux.HandleFunc("POST /api/v1/projects/{project}/search", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("project") == "err" {
			w.WriteHeader(500)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "internal"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"project": r.PathValue("project"), "model": "bge-m3", "fallback": false,
			"results": []map[string]any{
				{"path": "main.go", "start_line": 1, "score": 0.95, "content": "func main()"},
			},
		})
	})
	mux.HandleFunc("POST /api/v1/projects/{project}/index-jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("project") == "fail" {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "bad request"})
			return
		}
		w.WriteHeader(202)
		_ = json.NewEncoder(w).Encode(map[string]any{"job_id": 1, "status": "queued"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := client.New(srv.URL, "tok")
	b := NewClientBackend(c)

	// Projects.
	projects, err := b.Projects(context.Background())
	if err != nil {
		t.Fatalf("clientBackend.Projects: %v", err)
	}
	if len(projects) != 1 || projects[0].Name != "app" {
		t.Errorf("projects = %+v", projects)
	}

	// Search.
	out, err := b.Search(context.Background(), "app", "main func", "", 5, false, 0)
	if err != nil {
		t.Fatalf("clientBackend.Search: %v", err)
	}
	if out.Project != "app" || len(out.Results) != 1 || out.Results[0].Path != "main.go" {
		t.Errorf("search output = %+v", out)
	}

	// Search error.
	_, err = b.Search(context.Background(), "err", "x", "", 5, false, 0)
	if err == nil {
		t.Error("expected error from backend, got nil")
	}

	// Reindex error.
	_, err = b.Reindex(context.Background(), "fail", "full")
	if err == nil {
		t.Error("expected reindex error, got nil")
	}

	// Reindex success.
	msg, err := b.Reindex(context.Background(), "app", "full")
	if err != nil || !strings.Contains(msg, "#1") {
		t.Errorf("reindex = %q, err %v; want job id #1", msg, err)
	}
}
