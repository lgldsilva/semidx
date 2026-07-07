// Package searchtargets resolves which project(s) to search and runs queries,
// shared by the CLI (cmd is excluded from Sonar coverage; logic lives here).
package searchtargets

import (
	"context"
	"fmt"
	"os"

	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/projectref"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/pkg/client"
)

// osGetwd is os.Getwd; overridden in tests to exercise the error branch.
var osGetwd = os.Getwd

// ProjectLister lists remote projects for ref resolution.
type ProjectLister interface {
	ListProjects(ctx context.Context) ([]client.Project, error)
}

// RemoteSearcher runs a search against a remote server project.
type RemoteSearcher interface {
	Search(ctx context.Context, project, query, model string, topK int) (*client.SearchResponse, error)
}

// NamedResult is one project's search outcome.
type NamedResult struct {
	Name string
	Resp *search.Response
}

// ResolveProjects turns the --project argument (or its absence) into projects to search.
func ResolveProjects(ctx context.Context, db store.IndexStore, projectArg, cwd string) ([]*store.Project, error) {
	if projectArg != "" {
		p, err := projectref.Resolve(ctx, db, projectArg)
		if err != nil {
			return nil, fmt.Errorf("project not found: %s (index it, or pass a path/name that exists)", projectArg)
		}
		return []*store.Project{p}, nil
	}
	if cwd == "" {
		var err error
		cwd, err = osGetwd()
		if err != nil {
			return nil, err
		}
	}
	if gi := gitmeta.Resolve(ctx, cwd); gi.IsGit {
		if p, err := db.GetProjectByIdentity(ctx, gi.Identity); err == nil {
			return []*store.Project{p}, nil
		}
	}
	projects, err := db.ListProjects(ctx, 0, 0)
	if err != nil {
		return nil, err
	}
	if p := projectref.Enclosing(cwd, projects); p != nil {
		return []*store.Project{p}, nil
	}
	unique := projectref.UniqueByIdentity(projects)
	if len(unique) == 0 {
		return nil, fmt.Errorf("no indexed projects found — run 'semidx index --project .' first")
	}
	all := make([]*store.Project, len(unique))
	for i := range unique {
		all[i] = &unique[i]
	}
	return all, nil
}

// FromClientProjects maps SDK projects into store.Project values.
func FromClientProjects(projects []client.Project) []store.Project {
	out := make([]store.Project, len(projects))
	for i, p := range projects {
		out[i] = store.Project{
			Name: p.Name, Model: p.Model, Status: p.Status,
			SourceType: p.SourceType, GitURL: p.GitURL, Branch: p.Branch,
			Identity: p.Identity, Path: p.Path,
		}
	}
	return out
}

// ResolveRemoteProject resolves a user ref against the server's project list.
func ResolveRemoteProject(ctx context.Context, lister ProjectLister, ref string) (*store.Project, error) {
	if ref == "" {
		return nil, fmt.Errorf("--project is required in remote mode")
	}
	projects, err := lister.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	p, err := projectref.ResolveInList(ctx, ref, "", FromClientProjects(projects))
	if err != nil {
		return nil, fmt.Errorf("project not found: %s (index it, or pass a path/name that exists)", ref)
	}
	return p, nil
}

// SearchLocal runs a query against each resolved local target.
func SearchLocal(ctx context.Context, db store.IndexStore, emb embed.Embedder, targets []*store.Project, req search.Request, cwdGit gitmeta.Info) ([]NamedResult, error) {
	svc := search.NewService(db, emb)
	out := make([]NamedResult, 0, len(targets))
	for _, p := range targets {
		one := req
		if p.Identity != "" {
			one.Identity = p.Identity
			one.Project = ""
		} else {
			one.Project = p.Name
			one.Identity = ""
		}
		if p.SourceType == "git" && cwdGit.IsGit && cwdGit.Identity == p.Identity {
			one.Worktree = cwdGit.Toplevel
		}
		resp, err := svc.Search(ctx, one)
		if err != nil {
			return nil, err
		}
		out = append(out, NamedResult{Name: p.Name, Resp: resp})
	}
	return out, nil
}
