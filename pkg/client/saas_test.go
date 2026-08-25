package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// saasMux serves the SaaS-seam endpoints (dependency catalog, runtime graph,
// privacy, usage) with fixed payloads and records the last request it saw.
type saasRequest struct {
	method string
	path   string
	query  string
	body   map[string]any
}

func saasClient(t *testing.T, got *saasRequest, opts ...Option) *Client {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.query = r.Method, r.URL.Path, r.URL.RawQuery
		got.body = nil
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&got.body)
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/dependencies"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"dependencies": []Dependency{{Ecosystem: "go", Name: "github.com/x/y"}},
			})
		case strings.HasSuffix(r.URL.Path, "/dependencies/shared"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"dependencies": []DependencyUsage{{ProjectName: "other", Name: "github.com/x/y"}},
			})
		case strings.HasSuffix(r.URL.Path, "/dependencies/resolve"):
			_ = json.NewEncoder(w).Encode(DependencyResolveResponse{Mode: "managed", Status: "queued", JobID: 7})
		case strings.HasSuffix(r.URL.Path, "/dependencies/submit"):
			_ = json.NewEncoder(w).Encode(DependencySubmitResponse{Status: "ready", Count: 1})
		case strings.HasSuffix(r.URL.Path, "/runtime-edges") && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]any{"accepted": 2})
		case strings.HasSuffix(r.URL.Path, "/runtime-edges"),
			strings.HasSuffix(r.URL.Path, "/runtime-graph"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"edges": []RuntimeEdge{{TargetProjectName: "other", Protocol: "http"}},
			})
		case strings.HasSuffix(r.URL.Path, "/privacy"):
			_ = json.NewEncoder(w).Encode(Project{Name: "repo", PrivacyMode: "edge"})
		case strings.HasSuffix(r.URL.Path, "/search-usage"):
			_ = json.NewEncoder(w).Encode(UsageReport{Total: 3})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{})
		}
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(srv.URL, "tok", opts...)
}

func TestWithClientSourceSetsHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get(HeaderClientSource)
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", WithClientSource("cli"))
	if _, err := c.ListProjects(context.Background()); err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if got != "cli" {
		t.Errorf("%s = %q, want cli", HeaderClientSource, got)
	}
}

func TestSearchUsage(t *testing.T) {
	var got saasRequest
	c := saasClient(t, &got)

	report, err := c.SearchUsage(context.Background(), 7, "repo name")
	if err != nil {
		t.Fatalf("search usage: %v", err)
	}
	if report.Total != 3 {
		t.Errorf("total = %d, want 3", report.Total)
	}
	if got.path != "/api/v1/search-usage" {
		t.Errorf("path = %q", got.path)
	}
	if !strings.Contains(got.query, "days=7") || !strings.Contains(got.query, "project=repo+name") {
		t.Errorf("query = %q, want days and an escaped project", got.query)
	}

	// A non-positive window falls back to the documented 30-day default.
	if _, err := c.SearchUsage(context.Background(), 0, ""); err != nil {
		t.Fatalf("default window: %v", err)
	}
	if !strings.Contains(got.query, "days=30") || strings.Contains(got.query, "project=") {
		t.Errorf("query = %q, want days=30 and no project filter", got.query)
	}
}

func TestSearchMultiValidatesArguments(t *testing.T) {
	var got saasRequest
	c := saasClient(t, &got)
	ctx := context.Background()

	if _, err := c.SearchMulti(ctx, "  ", MultiSearchParams{All: true}); err == nil {
		t.Error("empty query must be rejected client-side")
	}
	if _, err := c.SearchMulti(ctx, "q", MultiSearchParams{}); err == nil {
		t.Error("no projects and all=false must be rejected client-side")
	}
	if _, err := c.SearchMulti(ctx, "q", MultiSearchParams{Projects: []string{"repo"}, TopK: 5}); err != nil {
		t.Fatalf("multi search: %v", err)
	}
	if got.path != "/api/v1/search" || got.body["query"] != "q" {
		t.Errorf("request = %+v", got)
	}
}

func TestDependencyCatalogClient(t *testing.T) {
	var got saasRequest
	c := saasClient(t, &got)
	ctx := context.Background()

	deps, err := c.ListDependencies(ctx, "repo")
	if err != nil || len(deps) != 1 {
		t.Fatalf("list dependencies: %v (%d)", err, len(deps))
	}
	if got.path != "/api/v1/projects/repo/dependencies" {
		t.Errorf("path = %q", got.path)
	}

	shared, err := c.SharedDependencies(ctx, "repo")
	if err != nil || len(shared) != 1 {
		t.Fatalf("shared dependencies: %v (%d)", err, len(shared))
	}

	resolved, err := c.ResolveDependencies(ctx, "repo", "managed")
	if err != nil || resolved.JobID != 7 {
		t.Fatalf("resolve dependencies: %v (%+v)", err, resolved)
	}
	if got.body["mode"] != "managed" {
		t.Errorf("mode = %v", got.body["mode"])
	}

	submitted, err := c.SubmitDependencies(ctx, "repo", []Dependency{{Ecosystem: "go", Name: "github.com/x/y"}}, "agent")
	if err != nil || submitted.Count != 1 {
		t.Fatalf("submit dependencies: %v (%+v)", err, submitted)
	}
	if got.body["source"] != "agent" {
		t.Errorf("source = %v", got.body["source"])
	}
}

func TestRuntimeGraphClient(t *testing.T) {
	var got saasRequest
	c := saasClient(t, &got)
	ctx := context.Background()

	edges, err := c.ListRuntimeEdges(ctx, "repo")
	if err != nil || len(edges) != 1 {
		t.Fatalf("list runtime edges: %v (%d)", err, len(edges))
	}

	accepted, err := c.SubmitRuntimeEdges(ctx, "repo", []RuntimeEdge{{TargetProjectName: "other"}})
	if err != nil || accepted != 2 {
		t.Fatalf("submit runtime edges: %v (%d)", err, accepted)
	}

	graph, err := c.ListRuntimeGraph(ctx, 25)
	if err != nil || len(graph) != 1 {
		t.Fatalf("list runtime graph: %v (%d)", err, len(graph))
	}
	if got.query != "limit=25" {
		t.Errorf("query = %q, want limit=25", got.query)
	}
}

func TestSetProjectPrivacyClient(t *testing.T) {
	var got saasRequest
	c := saasClient(t, &got)

	p, err := c.SetProjectPrivacy(context.Background(), "repo", "edge")
	if err != nil {
		t.Fatalf("set privacy: %v", err)
	}
	if p.PrivacyMode != "edge" || got.method != http.MethodPut {
		t.Errorf("project = %+v, method = %s", p, got.method)
	}
}

// Every project-scoped call rejects an empty project name before hitting the
// network, so a typo cannot be interpreted as a different route server-side.
func TestSaaSCallsRequireAProjectName(t *testing.T) {
	var got saasRequest
	c := saasClient(t, &got)
	ctx := context.Background()

	calls := map[string]func() error{
		"ListDependencies":   func() error { _, err := c.ListDependencies(ctx, " "); return err },
		"SharedDependencies": func() error { _, err := c.SharedDependencies(ctx, " "); return err },
		"ResolveDependencies": func() error {
			_, err := c.ResolveDependencies(ctx, " ", "managed")
			return err
		},
		"SubmitDependencies": func() error { _, err := c.SubmitDependencies(ctx, " ", nil, ""); return err },
		"ListRuntimeEdges":   func() error { _, err := c.ListRuntimeEdges(ctx, " "); return err },
		"SubmitRuntimeEdges": func() error { _, err := c.SubmitRuntimeEdges(ctx, " ", nil); return err },
		"SetProjectPrivacy":  func() error { _, err := c.SetProjectPrivacy(ctx, " ", "edge"); return err },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil {
				t.Errorf("%s accepted a blank project name", name)
			}
		})
	}
}
