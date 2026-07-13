package agent

import (
	"context"
	"strings"
	"testing"

	"charm.land/fantasy"

	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
)

func TestSearchScope_helpers(t *testing.T) {
	if !(SearchScope{}).IsZero() {
		t.Error("empty scope should be zero")
	}
	if (SearchScope{Project: "p"}).IsZero() {
		t.Error("scope with a project is not zero")
	}
	s := SearchScope{Project: "acme", Identity: "id-1"}
	if !s.Matches("acme") || !s.Matches("id-1") {
		t.Error("scope should match its project name and identity")
	}
	if s.Matches("other") {
		t.Error("scope must not match an unrelated ref")
	}
	if s.Label() != "acme" {
		t.Errorf("Label = %q, want project name", s.Label())
	}
	if (SearchScope{Identity: "id-only"}).Label() != "id-only" {
		t.Error("Label should fall back to identity")
	}
}

func TestContextWithScope_roundtrip(t *testing.T) {
	if _, ok := scopeFromContext(context.Background()); ok {
		t.Error("bare context should carry no scope")
	}
	ctx := ContextWithScope(context.Background(), SearchScope{Project: "p"})
	got, ok := scopeFromContext(ctx)
	if !ok || got.Project != "p" {
		t.Errorf("round-trip scope = %+v ok=%v", got, ok)
	}
}

// TestSearchTool_scopeContract is the core Fase 1 check: a scoped turn pins
// semantic_search to the bound project (when omitted) and refuses a conflicting
// explicit project — the scope is a contract, not a prompt hint.
func TestSearchTool_scopeContract(t *testing.T) {
	fs := newFakeSearchStore()
	fs.addProject(&store.Project{Name: "acme", Identity: "id-acme", SourceType: "git", Model: "m", Status: "ready"})
	svc := search.NewService(fs, fakeEmbedder{})
	tool := newSearchToolF(svc)

	// Omitted project + scope → searches the scoped project.
	ctx := ContextWithScope(t.Context(), SearchScope{Project: "acme"})
	resp, err := tool.Run(ctx, fantasy.ToolCall{Input: `{"query":"x"}`})
	if err != nil {
		t.Fatalf("Run(omitted): %v", err)
	}
	if resp.IsError {
		t.Fatalf("omitted project under scope should succeed, got error: %s", resp.Content)
	}
	if !strings.Contains(resp.Content, `"project":"acme"`) {
		t.Errorf("search should be scoped to acme, got: %s", resp.Content)
	}

	// Conflicting explicit project + scope → refused, and svc.Search not reached.
	resp, err = tool.Run(ctx, fantasy.ToolCall{Input: `{"query":"x","project":"other"}`})
	if err != nil {
		t.Fatalf("Run(conflict): %v", err)
	}
	if !resp.IsError || !strings.Contains(resp.Content, "scoped to project") {
		t.Errorf("conflicting project must be refused, got isErr=%v: %s", resp.IsError, resp.Content)
	}

	// Matching explicit project + scope → allowed.
	resp, _ = tool.Run(ctx, fantasy.ToolCall{Input: `{"query":"x","project":"acme"}`})
	if resp.IsError {
		t.Errorf("matching explicit project should be allowed, got: %s", resp.Content)
	}
}

// TestSearchTool_allProjectsScope is the Fase 6 check: an All scope fans
// semantic_search across every project and tags each hit with its project.
func TestSearchTool_allProjectsScope(t *testing.T) {
	if (SearchScope{All: true}).IsZero() {
		t.Fatal("a scope with All set must not be zero")
	}
	fs := newFakeSearchStore()
	fs.addProject(&store.Project{Name: "acme", Identity: "id-acme", SourceType: "git", Model: "m", Status: "ready"})
	fs.addProject(&store.Project{Name: "beta", Identity: "id-beta", SourceType: "git", Model: "m", Status: "ready"})
	svc := search.NewService(fs, fakeEmbedder{})
	tool := newSearchToolF(svc)

	ctx := ContextWithScope(t.Context(), SearchScope{All: true})
	resp, err := tool.Run(ctx, fantasy.ToolCall{Input: `{"query":"x"}`})
	if err != nil {
		t.Fatalf("Run(all): %v", err)
	}
	if resp.IsError {
		t.Fatalf("all-projects search should succeed, got error: %s", resp.Content)
	}
	if !strings.Contains(resp.Content, `"scope":"all-projects"`) {
		t.Errorf("expected the all-projects scope marker, got: %s", resp.Content)
	}
	if !strings.Contains(resp.Content, `"project":"id-acme"`) &&
		!strings.Contains(resp.Content, `"project":"id-beta"`) {
		t.Errorf("expected project-tagged hits, got: %s", resp.Content)
	}
}

// TestRunner_appliesDefaultScope verifies the Runner injects its configured
// default scope into the context (used by the ChatRAG REPL) when the caller
// didn't set one, while a caller-set scope wins.
func TestRunner_appliesDefaultScope(t *testing.T) {
	r := NewRunner(&fakeModel{}, nil, RunnerConfig{Scope: SearchScope{Project: "default"}})

	// No caller scope → default applied.
	ctx := r.applyScope(context.Background())
	if s, ok := scopeFromContext(ctx); !ok || s.Project != "default" {
		t.Errorf("default scope not applied: %+v ok=%v", s, ok)
	}
	// Caller scope present → preserved (default does not override).
	ctx = ContextWithScope(context.Background(), SearchScope{Project: "caller"})
	ctx = r.applyScope(ctx)
	if s, _ := scopeFromContext(ctx); s.Project != "caller" {
		t.Errorf("caller scope should win, got %q", s.Project)
	}
}
