package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/projectref"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/pkg/client"
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
// Remote mode resolves the ref against the server's project list (name, path,
// identity) before calling the API.
func (d *deps) runSearchTargets(cmd *cobra.Command, projectArg, query, model string, topK int, privacy bool) ([]projSearch, error) {
	ctx := cmd.Context()

	graph, _ := cmd.Flags().GetBool("graph")
	graphDepth, _ := cmd.Flags().GetInt("graph-depth")

	if d.remote() {
		if projectArg == "" {
			return nil, fmt.Errorf("--project is required in remote mode")
		}
		api := d.apiClient()
		projects, err := api.ListProjects(ctx)
		if err != nil {
			return nil, fmt.Errorf("list projects: %w", err)
		}
		p, err := projectref.ResolveInList(ctx, projectArg, "", clientProjectsToStore(projects))
		if err != nil {
			return nil, fmt.Errorf("project not found: %s (index it, or pass a path/name that exists)", projectArg)
		}
		resp, err := api.Search(ctx, p.Name, query, model, topK)
		if err != nil {
			return nil, err
		}
		return []projSearch{{p.Name, remoteToResponse(resp), time.Duration(resp.TookMS) * time.Millisecond}}, nil
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
		req := search.Request{Query: query, Model: model, TopK: topK, KeywordOnly: d.keywordOnly, Graph: graph, GraphMaxDepth: graphDepth}
		if p.Identity != "" {
			req.Identity = p.Identity
		} else {
			req.Project = p.Name
		}
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
		p, err := projectref.Resolve(ctx, db, projectArg)
		if err != nil {
			return nil, fmt.Errorf("project not found: %s (index it, or pass a path/name that exists)", projectArg)
		}
		return []*store.Project{p}, nil
	}

	// No project given: auto-detect the one enclosing the current directory.
	cwd, _ := os.Getwd()
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
	// Nothing encloses the cwd → search everything (one pass per identity).
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

func clientProjectsToStore(projects []client.Project) []store.Project {
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
