package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
)

// renderSearchJSON emits one project's results via the standard JSONFormatter, or
// a {"projects":[…]} array when several projects were searched.
func renderSearchJSON(w io.Writer, results []projSearch) error {
	if len(results) == 1 {
		return search.JSONFormatter{}.Format(w, results[0].resp)
	}
	type row struct {
		File    string  `json:"file"`
		Score   float64 `json:"score"`
		Content string  `json:"content"`
	}
	type proj struct {
		Project  string `json:"project"`
		Model    string `json:"model"`
		Fallback bool   `json:"fallback"`
		Results  []row  `json:"results"`
	}
	out := struct {
		Projects []proj `json:"projects"`
	}{Projects: []proj{}}
	for _, ps := range results {
		p := proj{Project: ps.name, Model: ps.resp.Model, Fallback: ps.resp.Fallback, Results: []row{}}
		for _, r := range ps.resp.Results {
			p.Results = append(p.Results, row{File: r.FilePath, Score: r.Score, Content: r.Content})
		}
		out.Projects = append(out.Projects, p)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// projSearch is one project's search outcome (used to render single- and
// multi-project output uniformly).
type projSearch struct {
	name string
	resp *search.Response
	took time.Duration
}

// runSearchTargets resolves which project(s) to search and runs the query
// against each. Resolution (local mode):
//   - a --project PATH resolves by unique identity (git identity or "path:<abs>"),
//     so same-basename folders never collide; a bare name still works as a fallback;
//   - with no --project, the project enclosing the current directory is used;
//   - if none encloses the cwd, ALL projects are searched, labeled per project.
//
// Remote mode keeps the name-based single-project flow (--project required).
func (d *deps) runSearchTargets(cmd *cobra.Command, projectArg, query, model string, topK int, privacy bool) ([]projSearch, error) {
	ctx := cmd.Context()

	if d.remote() {
		if projectArg == "" {
			return nil, fmt.Errorf("--project is required in remote mode")
		}
		resp, err := d.apiClient().Search(ctx, projectArg, query, model, topK)
		if err != nil {
			return nil, err
		}
		return []projSearch{{projectArg, remoteToResponse(resp), time.Duration(resp.TookMS) * time.Millisecond}}, nil
	}

	d.applyPrivacy(privacy)
	db, err := d.indexStore(ctx)
	if err != nil {
		return nil, err
	}
	targets, err := d.resolveSearchProjects(ctx, db, projectArg)
	if err != nil {
		return nil, err
	}

	svc := search.NewService(db, d.emb)
	// The worktree filter only makes sense for the git project the cwd is in.
	var cwdGit gitmeta.Info
	if gi := gitmeta.Resolve(ctx, "."); gi.IsGit {
		cwdGit = gi
	}

	out := make([]projSearch, 0, len(targets))
	for _, p := range targets {
		req := search.Request{Identity: p.Identity, Query: query, Model: model, TopK: topK, KeywordOnly: d.cfg.KeywordOnly}
		if p.SourceType == "git" && cwdGit.IsGit && cwdGit.Identity == p.Identity {
			req.Worktree = cwdGit.Toplevel
		}
		start := time.Now()
		resp, serr := svc.Search(ctx, req)
		if serr != nil {
			return nil, serr
		}
		out = append(out, projSearch{p.Name, resp, time.Since(start)})
	}
	return out, nil
}

// resolveSearchProjects turns the --project argument (or its absence) into the
// list of projects to search.
func (d *deps) resolveSearchProjects(ctx context.Context, db store.IndexStore, projectArg string) ([]*store.Project, error) {
	if projectArg != "" {
		if p := lookupByPathOrName(ctx, db, projectArg); p != nil {
			return []*store.Project{p}, nil
		}
		return nil, fmt.Errorf("project not found: %s (index it, or pass a path/name that exists)", projectArg)
	}

	// No project given: auto-detect the one enclosing the current directory.
	cwd, _ := os.Getwd()
	if gi := gitmeta.Resolve(ctx, cwd); gi.IsGit {
		if p, err := db.GetProjectByIdentity(ctx, gi.Identity); err == nil {
			return []*store.Project{p}, nil
		}
	}
	projects, err := db.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	if p := enclosingProject(cwd, projects); p != nil {
		return []*store.Project{p}, nil
	}
	// Nothing encloses the cwd → search everything.
	if len(projects) == 0 {
		return nil, fmt.Errorf("no indexed projects found")
	}
	all := make([]*store.Project, len(projects))
	for i := range projects {
		all[i] = &projects[i]
	}
	return all, nil
}

// lookupByPathOrName resolves an argument that may be a filesystem path (git repo
// or document folder) or a bare project name, trying the unique identities first.
func lookupByPathOrName(ctx context.Context, db store.IndexStore, arg string) *store.Project {
	// As a git repo path.
	if gi := gitmeta.Resolve(ctx, arg); gi.IsGit {
		if p, err := db.GetProjectByIdentity(ctx, gi.Identity); err == nil {
			return p
		}
	}
	// As a document folder path ("path:<abs>").
	if abs, err := filepath.Abs(arg); err == nil {
		if p, err := db.GetProjectByIdentity(ctx, "path:"+abs); err == nil {
			return p
		}
	}
	// As a bare name (backward-compatible).
	if p, err := db.GetProject(ctx, arg); err == nil {
		return p
	}
	return nil
}

// enclosingProject returns the project whose indexed path is the longest prefix
// of cwd (so running a search from inside an indexed folder finds it).
func enclosingProject(cwd string, projects []store.Project) *store.Project {
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
