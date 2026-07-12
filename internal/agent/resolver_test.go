package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

// fakeProjectStore implements the subset of store.IndexStore needed by
// ScopeResolver. It embeds the full interface for compile-time satisfaction
// and panics if an unimplemented method is called.
type fakeProjectStore struct {
	store.IndexStore
	projects map[string]*store.Project // keyed by name or identity
}

func (f *fakeProjectStore) GetProject(_ context.Context, name string) (*store.Project, error) {
	if p, ok := f.projects[name]; ok {
		return p, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeProjectStore) GetProjectByIdentity(_ context.Context, identity string) (*store.Project, error) {
	if p, ok := f.projects[identity]; ok {
		return p, nil
	}
	return nil, store.ErrNotFound
}

func TestScopeResolver_ResolveByName(t *testing.T) {
	db := &fakeProjectStore{
		projects: map[string]*store.Project{
			"my-project": {
				ID:         1,
				Name:       "my-project",
				Path:       "/home/user/projects/my-project",
				Identity:   "remote:github.com/user/my-project",
				SourceType: "git",
				Status:     "ready",
				Model:      "text-embedding-3-small",
			},
		},
	}
	resolver := NewScopeResolver(db)
	scope, err := resolver.Resolve(ctx, "my-project")
	if err != nil {
		t.Fatalf("Resolve by name: %v", err)
	}
	if scope.Path != "/home/user/projects/my-project" {
		t.Errorf("scope.Path = %q, want %q", scope.Path, "/home/user/projects/my-project")
	}
	if scope.Identity != "remote:github.com/user/my-project" {
		t.Errorf("scope.Identity = %q, want %q", scope.Identity, "remote:github.com/user/my-project")
	}
	if scope.Source != "git" {
		t.Errorf("scope.Source = %q, want %q", scope.Source, "git")
	}
}

func TestScopeResolver_ResolveByIdentity(t *testing.T) {
	db := &fakeProjectStore{
		projects: map[string]*store.Project{
			"remote:github.com/user/my-project": {
				ID:         1,
				Name:       "my-project",
				Path:       "/home/user/projects/my-project",
				Identity:   "remote:github.com/user/my-project",
				SourceType: "git",
			},
		},
	}
	resolver := NewScopeResolver(db)
	scope, err := resolver.Resolve(ctx, "remote:github.com/user/my-project")
	if err != nil {
		t.Fatalf("Resolve by identity: %v", err)
	}
	if scope.Identity != "remote:github.com/user/my-project" {
		t.Errorf("scope.Identity = %q", wantIdentity)
	}
}

func TestScopeResolver_ResolveNotFound(t *testing.T) {
	db := &fakeProjectStore{
		projects: map[string]*store.Project{},
	}
	resolver := NewScopeResolver(db)
	_, err := resolver.Resolve(ctx, "nonexistent-project")
	if err == nil {
		t.Fatal("expected error for nonexistent project, got nil")
	}
	if !errors.Is(err, store.ErrNotFound) {
		// The resolver wraps the error, so check the message instead.
		if err.Error() != "project not found: nonexistent-project" {
			t.Errorf("unexpected error: %v", err)
		}
	}
}

func TestScopeResolver_DocsProject(t *testing.T) {
	db := &fakeProjectStore{
		projects: map[string]*store.Project{
			"my-docs": {
				ID:         2,
				Name:       "my-docs",
				Path:       "/var/docs",
				Identity:   "path:/var/docs",
				SourceType: "docs",
				Status:     "ready",
				Model:      "text-embedding-3-small",
			},
		},
	}
	resolver := NewScopeResolver(db)
	scope, err := resolver.Resolve(ctx, "my-docs")
	if err != nil {
		t.Fatalf("Resolve docs: %v", err)
	}
	if scope.Source != "docs" {
		t.Errorf("scope.Source = %q, want %q", scope.Source, "docs")
	}
	if scope.Path != "/var/docs" {
		t.Errorf("scope.Path = %q, want %q", scope.Path, "/var/docs")
	}
}

var wantIdentity = "remote:github.com/user/my-project"
