package agent

import (
	"context"
	"fmt"

	"github.com/lgldsilva/semidx/internal/store"
)

// Scope holds resolved project identity and type.
type Scope struct {
	Path     string // filesystem root ("" for pure-remote)
	Identity string // project identity
	Source   string // "git" or "docs"
}

// ScopeResolver maps a project reference to identity/path/source.
// Injected once into Agent; tools call it instead of each reimplementing
// projectref/gitmeta.
type ScopeResolver interface {
	Resolve(ctx context.Context, ref string) (*Scope, error)
}

// NewScopeResolver creates a ScopeResolver backed by an IndexStore.
func NewScopeResolver(db store.IndexStore) ScopeResolver {
	return &storeResolver{db: db}
}

type storeResolver struct {
	db store.IndexStore
}

func (r *storeResolver) Resolve(ctx context.Context, ref string) (*Scope, error) {
	p, err := r.db.GetProject(ctx, ref)
	if err != nil {
		p, err = r.db.GetProjectByIdentity(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("project not found: %s", ref)
		}
	}
	return &Scope{
		Path:     p.Path,
		Identity: p.Identity,
		Source:   p.SourceType,
	}, nil
}
