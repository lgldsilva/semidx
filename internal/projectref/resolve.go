// Package projectref resolves project references the same way across the CLI,
// HTTP API, and admin UI: by git identity, document path identity, filesystem
// path, exact name, or case-insensitive name.
package projectref

import (
	"context"
	"fmt"
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
	projects, err := db.ListProjects(ctx, 0, 0)
	if err == nil && len(projects) > 0 {
		return resolveInList(ref, "", projects)
	}
	// Some IndexStore adapters intentionally implement direct lookup without
	// materializing a project list. Keep those adapters working while using the
	// list whenever it is available, because only the list can detect ambiguous
	// display names.
	if p, getErr := db.GetProject(ctx, ref); getErr == nil {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, store.ErrNotFound
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
	for _, path := range equivalentPathForms(arg) {
		if p, err := db.GetProjectByIdentity(ctx, "path:"+path); err == nil {
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
	for _, path := range equivalentPathForms(arg) {
		if p := findByIdentity(projects, "path:"+path); p != nil {
			return p
		}
	}
	return nil
}

func resolveInList(ref, cwd string, projects []store.Project) (*store.Project, error) {
	if p, err := findByName(ref, projects); p != nil || err != nil {
		return p, err
	}
	if p := findByIdentity(projects, ref); p != nil {
		return p, nil
	}
	if p := findByIndexedPath(ref, projects); p != nil {
		return p, nil
	}
	if cwd != "" {
		if p := Enclosing(cwd, projects); p != nil {
			return p, nil
		}
	}
	return nil, store.ErrNotFound
}

func findByName(ref string, projects []store.Project) (*store.Project, error) {
	var match *store.Project
	var identities []string
	for i := range projects {
		if projects[i].Name == ref || strings.EqualFold(projects[i].Name, ref) {
			if match == nil {
				match = &projects[i]
			}
			identity := projects[i].Identity
			if identity == "" {
				identity = projects[i].Path
			}
			identities = append(identities, identity)
		}
	}
	if len(identities) > 1 {
		return nil, fmt.Errorf("ambiguous project name %q; use a path or identity (%s)", ref, strings.Join(identities, ", "))
	}
	return match, nil
}

func findByIndexedPath(ref string, projects []store.Project) *store.Project {
	refPath, ok := canonicalPath(ref)
	if !ok {
		return nil
	}
	for i := range projects {
		projectPath, valid := canonicalPath(projects[i].Path)
		if valid && projectPath == refPath {
			return &projects[i]
		}
	}
	return nil
}

func findByIdentity(projects []store.Project, identity string) *store.Project {
	if identity == "" {
		return nil
	}
	for i := range projects {
		if projects[i].Identity == identity {
			return &projects[i]
		}
	}
	return nil
}

// Enclosing returns the project whose indexed path is the longest prefix of cwd.
func Enclosing(cwd string, projects []store.Project) *store.Project {
	cwdPath, ok := canonicalPath(cwd)
	if !ok {
		return nil
	}
	var best *store.Project
	bestLen := -1
	for i := range projects {
		projectPath, valid := canonicalPath(projects[i].Path)
		if !valid {
			continue
		}
		if cwdPath == projectPath || strings.HasPrefix(cwdPath, projectPath+string(os.PathSeparator)) {
			if len(projectPath) > bestLen {
				bestLen = len(projectPath)
				best = &projects[i]
			}
		}
	}
	return best
}

// equivalentPathForms returns both the lexical absolute path and, when the
// filesystem can resolve it, the symlink-canonical path. Keeping both forms
// preserves identities already stored with aliases such as /tmp while allowing
// lookups from their canonical form such as /private/tmp.
func equivalentPathForms(path string) []string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	abs = filepath.Clean(abs)
	forms := []string{abs}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return forms
	}
	resolved = filepath.Clean(resolved)
	if resolved != abs {
		forms = append(forms, resolved)
	}
	return forms
}

func canonicalPath(path string) (string, bool) {
	forms := equivalentPathForms(path)
	if len(forms) == 0 {
		return "", false
	}
	return forms[len(forms)-1], true
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
