package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/clientconfig"
	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/searchtargets"
	"github.com/lgldsilva/semidx/pkg/client"
)

// projSearch is one project's search outcome (used to render single- and
// multi-project output uniformly).
type projSearch struct {
	name        string
	resp        *search.Response
	took        time.Duration
	projectPath string
}

// searchCall groups parameters for runRemoteSearch / runLocalSearch to keep
// parameter count ≤ 7 (addresses S107).
type searchCall struct {
	projectArg  string
	query       string
	model       string
	topK        int
	privacy     bool
	keywordOnly bool
	vectorOnly  bool
	graph       bool
	graphDepth  int
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
	graph, graphDepth := searchGraphOpts(cmd)
	call := searchCall{
		projectArg:  projectArg,
		query:       query,
		model:       model,
		topK:        topK,
		privacy:     privacy,
		keywordOnly: d.keywordOnly,
		vectorOnly:  d.vectorOnly,
		graph:       graph,
		graphDepth:  graphDepth,
	}
	if d.remote() {
		return d.runRemoteSearch(ctx, call)
	}
	return d.runLocalSearch(ctx, call)
}

func searchGraphOpts(cmd *cobra.Command) (graph bool, graphDepth int) {
	graph, _ = cmd.Flags().GetBool("graph")
	depth, _ := cmd.Flags().GetInt("graph-depth")
	return graph, search.ClampGraphDepth(depth)
}

func (d *deps) runRemoteSearch(ctx context.Context, call searchCall) ([]projSearch, error) {
	if call.vectorOnly {
		return nil, fmt.Errorf("vector-only benchmark is available in local or postgres mode; remote API does not expose an isolated vector retriever")
	}
	api := d.searchAPI()
	projectRef := remoteSearchProjectRef(call.projectArg, d.client)
	p, err := searchtargets.ResolveRemoteProject(ctx, api, projectRef)
	if err != nil {
		return nil, err
	}
	resp, err := api.Search(ctx, p.Name, call.query, client.SearchParams{
		Model: call.model, TopK: call.topK, Keyword: call.keywordOnly,
		Graph: call.graph, GraphDepth: call.graphDepth,
	})
	if err != nil {
		return nil, err
	}
	return []projSearch{{name: p.Name, resp: remoteToResponse(resp, p.Path), took: time.Duration(resp.TookMS) * time.Millisecond, projectPath: p.Path}}, nil
}

func remoteSearchProjectRef(projectArg string, cfg *clientconfig.Config) string {
	if strings.TrimSpace(projectArg) != "" || cfg == nil {
		return projectArg
	}
	return strings.TrimSpace(cfg.DefaultProject)
}

func (d *deps) runLocalSearch(ctx context.Context, call searchCall) ([]projSearch, error) {
	d.applyPrivacy(call.privacy)
	db, err := d.indexStore(ctx)
	if err != nil {
		return nil, err
	}
	targets, err := searchtargets.ResolveProjects(ctx, db, call.projectArg, "")
	if err != nil {
		return nil, err
	}
	var cwdGit gitmeta.Info
	if gi := gitmeta.Resolve(ctx, "."); gi.IsGit {
		cwdGit = gi
	}
	req := search.Request{
		Query: call.query, Model: call.model, TopK: call.topK, KeywordOnly: d.keywordOnly,
		VectorOnly: d.vectorOnly,
		Graph:      call.graph, GraphMaxDepth: call.graphDepth,
	}
	results, err := searchtargets.SearchLocal(ctx, db, d.emb, targets, req, cwdGit)
	if err != nil {
		return nil, err
	}
	out := make([]projSearch, 0, len(results))
	for i, r := range results {
		projectPath := ""
		if i < len(targets) && targets[i] != nil {
			projectPath = targets[i].Path
			if targets[i].SourceType == "git" && cwdGit.IsGit && cwdGit.Identity == targets[i].Identity {
				projectPath = cwdGit.Toplevel
			}
		}
		if projectPath == "" && r.Resp != nil && r.Resp.Project != nil {
			projectPath = r.Resp.Project.Path
		}
		out = append(out, projSearch{name: r.Name, resp: r.Resp, took: time.Duration(r.Resp.TookMS) * time.Millisecond, projectPath: projectPath})
	}
	return out, nil
}

// renderSearchJSON delegates to searchtargets for test coverage (cmd is excluded).
func renderSearchJSON(w io.Writer, results []projSearch) error {
	named := make([]searchtargets.NamedResult, len(results))
	took := make([]time.Duration, len(results))
	for i, r := range results {
		named[i] = searchtargets.NamedResult{Name: r.name, Resp: r.resp}
		took[i] = r.took
	}
	return searchtargets.RenderSearchJSON(w, named, took)
}
