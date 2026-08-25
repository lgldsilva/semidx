// Package searchtargets resolves which project(s) to search and runs queries,
// shared by the CLI (cmd is excluded from Sonar coverage; logic lives here).
package searchtargets

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/projectref"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/internal/usage"
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
			if errors.Is(err, store.ErrNotFound) {
				return nil, fmt.Errorf("project not found: %s (index it, or pass a path/name that exists)", projectArg)
			}
			return nil, fmt.Errorf("resolve project %q: %w", projectArg, err)
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
// An empty ref resolves the project enclosing the current directory, matching
// what local mode already does (and what the --project help text promises),
// so the first call an agent makes does not have to name a project.
func ResolveRemoteProject(ctx context.Context, lister ProjectLister, ref string) (*store.Project, error) {
	projects, err := lister.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	stored := FromClientProjects(projects)
	cwd, cwdErr := osGetwd()
	if cwdErr != nil {
		cwd = ""
	}
	if ref == "" || ref == "." {
		// "." is the default of --project on some commands and means "here",
		// so it takes the same cwd resolution as an omitted flag.
		if p, err := resolveRemoteFromCwd(ctx, cwd, stored); err == nil {
			return p, nil
		} else if ref == "" {
			return nil, err
		}
	}
	p, err := projectref.ResolveInList(ctx, ref, cwd, stored)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("project not found: %s (%s)", ref, remoteProjectHint(stored))
		}
		return nil, fmt.Errorf("resolve remote project %q: %w", ref, err)
	}
	return p, nil
}

// resolveRemoteFromCwd mirrors the local cwd resolution, trying the strategies
// in descending order of confidence: the git repo identity, its normalized
// origin URL, the project whose indexed path encloses the directory, then the
// checkout's directory name.
func resolveRemoteFromCwd(ctx context.Context, cwd string, projects []store.Project) (*store.Project, error) {
	if cwd == "" {
		return nil, errUnknownCwdProject(projects)
	}
	gi := gitmeta.Resolve(ctx, cwd)
	if p := lookupUnambiguous(ctx, gi.Identity, projects); p != nil {
		return p, nil
	}
	// A server-side git project may have a user-chosen name as its identity.
	// Its clone path is unrelated to the caller's checkout, so compare the
	// normalized origin URL as a second stable locator before falling back to
	// filesystem heuristics.
	if p := lookupByGitOrigin(gi, projects); p != nil {
		return p, nil
	}
	if p := projectref.Enclosing(cwd, projects); p != nil {
		return p, nil
	}
	// Last resort: the checkout's directory name. A repo indexed from a
	// different remote than the one this clone uses (a mirror, or a host
	// migration) has an identity that cannot match, yet is plainly the same
	// project.
	if p := lookupUnambiguous(ctx, checkoutName(cwd, gi), projects); p != nil {
		return p, nil
	}
	return nil, errUnknownCwdProject(projects)
}

func lookupByGitOrigin(gi gitmeta.Info, projects []store.Project) *store.Project {
	if !gi.IsGit || !strings.HasPrefix(gi.Identity, "remote:") {
		return nil
	}
	want := normalizeGitOrigin(strings.TrimPrefix(gi.Identity, "remote:"))
	var match *store.Project
	for i := range projects {
		if projects[i].GitURL == "" || normalizeGitOrigin(projects[i].GitURL) != want {
			continue
		}
		if match != nil {
			// The same remote can legitimately have multiple registered
			// branches/projects; never guess between them.
			return nil
		}
		match = &projects[i]
	}
	return match
}

func normalizeGitOrigin(raw string) string {
	normalized := gitmeta.NormalizeRemote(raw)
	// Redacted scp-like remotes arrive from the API as host:org/repo,
	// whereas the local git identity is host/org/repo. Normalize the safe,
	// credential-free form without changing ordinary URL paths.
	colon := strings.IndexByte(normalized, ':')
	slash := strings.IndexByte(normalized, '/')
	if colon > 0 && (slash < 0 || colon < slash) {
		normalized = normalized[:colon] + "/" + normalized[colon+1:]
	}
	return normalized
}

// lookupUnambiguous resolves a ref, treating "not found" and "ambiguous" alike:
// neither is good enough to pick a project on the caller's behalf.
func lookupUnambiguous(ctx context.Context, ref string, projects []store.Project) *store.Project {
	if ref == "" {
		return nil
	}
	p, err := projectref.ResolveInList(ctx, ref, "", projects)
	if err != nil {
		return nil
	}
	return p
}

// checkoutName is the directory name identifying the checkout: the repository
// root when in git, else the directory itself. Empty when it names no project.
func checkoutName(cwd string, gi gitmeta.Info) string {
	root := cwd
	if gi.Toplevel != "" {
		root = gi.Toplevel
	}
	base := filepath.Base(root)
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

func errUnknownCwdProject(projects []store.Project) error {
	return fmt.Errorf("could not tell which project this directory belongs to (%s)", remoteProjectHint(projects))
}

// remoteProjectHint names the indexed projects so a failed resolution tells the
// caller what it may pass instead of only what went wrong.
func remoteProjectHint(projects []store.Project) string {
	unique := projectref.UniqueByIdentity(projects)
	if len(unique) == 0 {
		return "no projects are indexed on the server — run 'semidx push --project .'"
	}
	names := make([]string, 0, len(unique))
	for i := range unique {
		names = append(names, unique[i].Name)
	}
	sort.Strings(names)
	const maxNames = 10
	if len(names) > maxNames {
		return fmt.Sprintf("pass --project with one of %s … (%d more)",
			strings.Join(names[:maxNames], ", "), len(names)-maxNames)
	}
	return "pass --project with one of " + strings.Join(names, ", ")
}

// SearchLocal runs a query against each resolved local target.
func SearchLocal(ctx context.Context, db store.IndexStore, emb embed.Embedder, targets []*store.Project, req search.Request, cwdGit gitmeta.Info) ([]NamedResult, error) {
	svc := search.NewService(db, emb)
	if uw, ok := db.(usage.StoreWriter); ok {
		logQueries := os.Getenv("SEMIDX_USAGE_LOG_QUERIES") == "1" ||
			strings.EqualFold(os.Getenv("SEMIDX_USAGE_LOG_QUERIES"), "true")
		svc.WithUsage(&usage.StoreRecorder{Store: uw, LogQueries: logQueries})
	}
	ctx = usage.WithSource(ctx, usage.SourceCLI)
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
