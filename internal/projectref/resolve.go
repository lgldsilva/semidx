// Package projectref resolves project references the same way across the CLI,
// HTTP API, and admin UI: by git identity, document path identity, filesystem
// path, exact name, or case-insensitive name.
package projectref

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/store"
)

// Resolve finds an indexed project from a user-supplied reference.
func Resolve(ctx context.Context, db store.IndexStore, ref string) (*store.Project, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, store.ErrNotFound
	}
	if p := lookupByPathOrIdentity(ctx, db, ref); p != nil {
		return p, nil
	}
	if p, err := db.GetProject(ctx, ref); err == nil {
		return p, nil
	}
	projects, err := db.ListProjects(ctx, 0, 0)
	if err != nil {
		return nil, err
	}
	return resolveInList(ref, "", projects)
}

// ResolveInList resolves ref against a project list (remote API clients that
// already fetched /api/v1/projects). cwd is optional for enclosing-path lookup.
func ResolveInList(ctx context.Context, ref, cwd string, projects []store.Project) (*store.Project, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, store.ErrNotFound
	}
	if p := lookupInListByPathOrIdentity(ctx, ref, projects); p != nil {
		return p, nil
	}
	return resolveInList(ref, cwd, projects)
}

func lookupByPathOrIdentity(ctx context.Context, db store.IndexStore, arg string) *store.Project {
	if gi := gitmeta.Resolve(ctx, arg); gi.IsGit {
		if p, err := db.GetProjectByIdentity(ctx, gi.Identity); err == nil {
			return p
		}
	}
	if abs, err := filepath.Abs(arg); err == nil {
		if p, err := db.GetProjectByIdentity(ctx, "path:"+abs); err == nil {
			return p
		}
	}
	return nil
}

func lookupInListByPathOrIdentity(ctx context.Context, arg string, projects []store.Project) *store.Project {
	if gi := gitmeta.Resolve(ctx, arg); gi.IsGit {
		if p := findByIdentity(projects, gi.Identity); p != nil {
			return p
		}
	}
	if abs, err := filepath.Abs(arg); err == nil {
		if p := findByIdentity(projects, "path:"+abs); p != nil {
			return p
		}
	}
	return nil
}

func resolveInList(ref, cwd string, projects []store.Project) (*store.Project, error) {
	for i := range projects {
		if projects[i].Name == ref {
			return &projects[i], nil
		}
	}
	for i := range projects {
		if strings.EqualFold(projects[i].Name, ref) {
			return &projects[i], nil
		}
	}
	for i := range projects {
		if projects[i].Identity != "" && projects[i].Identity == ref {
			return &projects[i], nil
		}
	}
	if abs, err := filepath.Abs(ref); err == nil {
		for i := range projects {
			pp, perr := filepath.Abs(projects[i].Path)
			if perr == nil && pp == abs {
				return &projects[i], nil
			}
		}
	}
	if cwd != "" {
		if p := Enclosing(cwd, projects); p != nil {
			return p, nil
		}
	}
	return nil, store.ErrNotFound
}

func findByIdentity(projects []store.Project, identity string) *store.Project {
	for i := range projects {
		if projects[i].Identity == identity {
			return &projects[i]
		}
	}
	return nil
}

// Enclosing returns the project whose indexed path is the longest prefix of cwd.
func Enclosing(cwd string, projects []store.Project) *store.Project {
	var best *store.Project
	bestLen := -1
	for i := range projects {
		pp, err := filepath.Abs(projects[i].Path)
		if err != nil {
			continue
		}
		if cwd == pp || strings.HasPrefix(cwd, pp+string(os.PathSeparator)) {
			if len(pp) > bestLen {
				bestLen = len(pp)
				best = &projects[i]
			}
		}
	}
	return best
}

// UniqueByIdentity returns at most one project per non-empty identity so the
// same logical repo indexed under different display names is searched once.
func UniqueByIdentity(projects []store.Project) []store.Project {
	seen := make(map[string]struct{}, len(projects))
	out := make([]store.Project, 0, len(projects))
	for _, p := range projects {
		if p.Identity == "" {
			out = append(out, p)
			continue
		}
		if _, ok := seen[p.Identity]; ok {
			continue
		}
		seen[p.Identity] = struct{}{}
		out = append(out, p)
	}
	return out
}
