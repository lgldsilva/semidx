package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/graph"
	"github.com/lgldsilva/semidx/internal/projectref"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/pkg/client"
)

const (
	flagProjectHelp = "Project path or name"
	flagJSONHelp    = "Emit JSON"
)

func newGraphStatsCmd(d *deps) *cobra.Command {
	var projectArg string
	var asJSON bool
	c := &cobra.Command{
		Use:   "stats",
		Short: "Show file↔package graph size and top in/out-degree hubs",
		Long: `Report node/edge counts and the busiest files of the static dependency
graph (file → package imports).

Local-only: the server API exposes neighbors and path, not aggregate stats.`,
		Example: "  semidx graph stats --project .",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGraphStats(cmd.Context(), cmd.OutOrStdout(), d, projectArg, asJSON)
		},
	}
	c.Flags().StringVar(&projectArg, "project", "", flagProjectHelp)
	c.Flags().BoolVar(&asJSON, "json", false, flagJSONHelp)
	return c
}

func runGraphStats(ctx context.Context, w io.Writer, d *deps, projectArg string, asJSON bool) error {
	if d.remote() {
		return fmt.Errorf("graph stats over remote API is not yet exposed; use neighbors/path, or --local")
	}
	db, proj, err := resolveGraphProject(ctx, d, projectArg)
	if err != nil {
		return err
	}
	neighbors, err := db.FetchGraphNeighbors(ctx, proj.ID)
	if err != nil {
		return fmt.Errorf("fetch graph: %w", err)
	}
	outDeg, inDeg, edges, nodes := degreeMaps(neighbors)
	if asJSON {
		return encodeGraphJSON(w, map[string]any{
			"project":      proj.Name,
			"nodes":        nodes,
			"edges":        edges,
			"top_depends":  topDegreeEntries(outDeg, 10),
			"top_depended": topDegreeEntries(inDeg, 10),
		})
	}
	_, _ = fmt.Fprintf(w, "project %s: %d nodes, %d edges\n", proj.Name, nodes, edges)
	printDegreeBlock(w, "top outbound:", outDeg)
	printDegreeBlock(w, "top inbound:", inDeg)
	return nil
}

func degreeMaps(neighbors map[string][]string) (outDeg, inDeg map[string]int, edges, nodes int) {
	outDeg = map[string]int{}
	inDeg = map[string]int{}
	for src, targets := range neighbors {
		outDeg[src] = len(targets)
		edges += len(targets)
		for _, t := range targets {
			inDeg[t]++
		}
	}
	seen := map[string]struct{}{}
	for n := range outDeg {
		seen[n] = struct{}{}
	}
	for n := range inDeg {
		seen[n] = struct{}{}
	}
	return outDeg, inDeg, edges, len(seen)
}

func printDegreeBlock(w io.Writer, title string, deg map[string]int) {
	_, _ = fmt.Fprintln(w, title)
	for _, e := range topDegreeEntries(deg, 10) {
		_, _ = fmt.Fprintf(w, "  %s (%d)\n", e["node"], e["degree"])
	}
}

func newGraphNeighborsCmd(d *deps) *cobra.Command {
	var projectArg, file string
	var depth, limit int
	var asJSON bool
	c := &cobra.Command{
		Use:   "neighbors",
		Short: "Show the file↔package dependency neighborhood around a file",
		Long: `Expand the static dependency neighborhood (import and package-membership
edges) around a seed file.

In remote mode this calls the server API; with --local it reads the index
directly.`,
		Example: "  semidx graph neighbors --project . --file internal/store/store.go",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGraphNeighbors(cmd.Context(), cmd.OutOrStdout(), d, projectArg, file, depth, limit, asJSON)
		},
	}
	c.Flags().StringVar(&projectArg, "project", "", flagProjectHelp)
	c.Flags().StringVar(&file, "file", "", "Seed file path (project-relative)")
	c.Flags().IntVar(&depth, "depth", 0, "Max BFS depth (default 2)")
	c.Flags().IntVar(&limit, "limit", 0, "Max edges returned")
	c.Flags().BoolVar(&asJSON, "json", false, flagJSONHelp)
	return c
}

func runGraphNeighbors(ctx context.Context, w io.Writer, d *deps, projectArg, file string, depth, limit int, asJSON bool) error {
	if file == "" {
		return fmt.Errorf("--file is required")
	}
	if d.remote() {
		return runRemoteNeighbors(ctx, w, d, projectArg, file, depth, limit, asJSON)
	}
	return runLocalNeighbors(ctx, w, d, projectArg, file, depth, limit, asJSON)
}

func runRemoteNeighbors(ctx context.Context, w io.Writer, d *deps, projectArg, file string, depth, limit int, asJSON bool) error {
	name, err := remoteGraphProjectName(ctx, d, projectArg)
	if err != nil {
		return err
	}
	resp, err := d.apiClient().GraphSubgraph(ctx, name, file, depth, limit)
	if err != nil {
		return err
	}
	if asJSON {
		return encodeGraphJSON(w, resp)
	}
	printSubgraph(w, len(resp.Nodes), resp.Truncated, clientGraphEdgeLines(resp.Edges))
	return nil
}

func runLocalNeighbors(ctx context.Context, w io.Writer, d *deps, projectArg, file string, depth, limit int, asJSON bool) error {
	db, proj, err := resolveGraphProject(ctx, d, projectArg)
	if err != nil {
		return err
	}
	idx, err := loadLocalGraphIndex(ctx, db, proj.ID)
	if err != nil {
		return err
	}
	sg := idx.Subgraph(file, graph.Budget{MaxDepth: depth, MaxEdgesOut: limit})
	if asJSON {
		return encodeGraphJSON(w, sg)
	}
	printSubgraph(w, len(sg.Nodes), sg.Truncated, graphEdgeLines(sg.Edges))
	return nil
}

func newGraphPathCmd(d *deps) *cobra.Command {
	var projectArg, from, to string
	var maxDepth int
	var undirected, asJSON bool
	c := &cobra.Command{
		Use:   "path",
		Short: "Show how file A communicates with file B",
		Long: `Find a shortest path between two indexed files through import edges.

By default only directed dependency flow is used (A imports … toward B).
Pass --undirected to allow reverse hops; the result then reports directed=false
and edges may include reverse:true.`,
		Example: `  semidx graph path --from cmd/semidx/main.go --to internal/store/store.go
  semidx graph path --from a.go --to b.go --undirected --json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGraphPath(cmd.Context(), cmd.OutOrStdout(), d, projectArg, from, to, maxDepth, undirected, asJSON)
		},
	}
	c.Flags().StringVar(&projectArg, "project", "", flagProjectHelp)
	c.Flags().StringVar(&from, "from", "", "Source file (project-relative)")
	c.Flags().StringVar(&to, "to", "", "Target file (project-relative)")
	c.Flags().IntVar(&maxDepth, "max-depth", 0, "Max BFS depth (default 8)")
	c.Flags().BoolVar(&undirected, "undirected", false, "Allow reverse hops")
	c.Flags().BoolVar(&asJSON, "json", false, flagJSONHelp)
	return c
}

func runGraphPath(ctx context.Context, w io.Writer, d *deps, projectArg, from, to string, maxDepth int, undirected, asJSON bool) error {
	if from == "" || to == "" {
		return fmt.Errorf("--from and --to are required")
	}
	var found bool
	var err error
	if d.remote() {
		found, err = runRemotePath(ctx, w, d, projectArg, from, to, maxDepth, undirected, asJSON)
	} else {
		found, err = runLocalPath(ctx, w, d, projectArg, from, to, maxDepth, undirected, asJSON)
	}
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no path found from %s to %s", from, to)
	}
	return nil
}

func runRemotePath(ctx context.Context, w io.Writer, d *deps, projectArg, from, to string, maxDepth int, undirected, asJSON bool) (bool, error) {
	name, err := remoteGraphProjectName(ctx, d, projectArg)
	if err != nil {
		return false, err
	}
	resp, err := d.apiClient().GraphPath(ctx, name, from, to, maxDepth, undirected)
	if err != nil {
		return false, err
	}
	if err := emitPath(w, asJSON, resp, resp.Found, resp.Directed, resp.Truncated, resp.Hops, resp.Length); err != nil {
		return false, err
	}
	return resp.Found, nil
}

func runLocalPath(ctx context.Context, w io.Writer, d *deps, projectArg, from, to string, maxDepth int, undirected, asJSON bool) (bool, error) {
	db, proj, err := resolveGraphProject(ctx, d, projectArg)
	if err != nil {
		return false, err
	}
	idx, err := loadLocalGraphIndex(ctx, db, proj.ID)
	if err != nil {
		return false, err
	}
	pr := idx.ShortestPath(from, to, graph.Budget{MaxDepth: maxDepth}, undirected)
	if err := emitPath(w, asJSON, pr, pr.Found, pr.Directed, pr.Truncated, pr.Hops, pr.Length); err != nil {
		return false, err
	}
	return pr.Found, nil
}

func emitPath(w io.Writer, asJSON bool, payload any, found, directed, truncated bool, hops []string, length int) error {
	if asJSON {
		return encodeGraphJSON(w, payload)
	}
	printPathResult(w, found, directed, truncated, hops, length)
	return nil
}

func printSubgraph(w io.Writer, nodes int, truncated bool, edges []string) {
	_, _ = fmt.Fprintf(w, "%d nodes, %d edges", nodes, len(edges))
	if truncated {
		_, _ = fmt.Fprint(w, " (truncated)")
	}
	_, _ = fmt.Fprintln(w)
	for _, line := range edges {
		_, _ = fmt.Fprintf(w, "  %s\n", line)
	}
}

func graphEdgeLines(edges []graph.Edge) []string {
	out := make([]string, len(edges))
	for i, e := range edges {
		out[i] = fmt.Sprintf("%s -[%s]-> %s", e.Source, e.Kind, e.Target)
	}
	return out
}

func clientGraphEdgeLines(edges []client.GraphEdge) []string {
	out := make([]string, len(edges))
	for i, e := range edges {
		out[i] = fmt.Sprintf("%s -[%s]-> %s", e.Source, e.Kind, e.Target)
	}
	return out
}

func printPathResult(w io.Writer, found, directed, truncated bool, hops []string, length int) {
	if !found {
		_, _ = fmt.Fprintln(w, "not found")
		if truncated {
			_, _ = fmt.Fprintln(w, "(search truncated by budget)")
		}
		return
	}
	mode := "directed"
	if !directed {
		mode = "undirected"
	}
	_, _ = fmt.Fprintf(w, "%s path (%d hops):\n  %s\n", mode, length, strings.Join(hops, " → "))
}

func encodeGraphJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func topDegreeEntries(deg map[string]int, limit int) []map[string]any {
	type e struct {
		n string
		d int
	}
	list := make([]e, 0, len(deg))
	for n, d := range deg {
		list = append(list, e{n, d})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].d != list[j].d {
			return list[i].d > list[j].d
		}
		return list[i].n < list[j].n
	})
	if len(list) > limit {
		list = list[:limit]
	}
	out := make([]map[string]any, len(list))
	for i, x := range list {
		out[i] = map[string]any{"node": x.n, "degree": x.d}
	}
	return out
}

// resolveGraphProject picks the project to query: an explicit --project ref, the
// git project enclosing the cwd, or the project whose path encloses the cwd.
func resolveGraphProject(ctx context.Context, d *deps, projectArg string) (store.IndexStore, *store.Project, error) {
	db, err := d.indexStore(ctx)
	if err != nil {
		return nil, nil, err
	}
	if projectArg != "" {
		p, err := projectref.Resolve(ctx, db, projectArg)
		return db, p, err
	}
	if gi := gitmeta.Resolve(ctx, "."); gi.IsGit {
		if p, err := db.GetProjectByIdentity(ctx, gi.Identity); err == nil {
			return db, p, nil
		}
	}
	projects, err := db.ListProjects(ctx, 0, 0)
	if err != nil {
		return nil, nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, err
	}
	if p := projectref.Enclosing(cwd, projects); p != nil {
		return db, p, nil
	}
	return nil, nil, fmt.Errorf("no project found; pass --project")
}

func loadLocalGraphIndex(ctx context.Context, db store.IndexStore, projectID int) (*graph.Index, error) {
	neighbors, err := db.FetchGraphNeighbors(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("fetch graph: %w", err)
	}
	hashes, err := db.ListFileHashes(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	files := make([]string, 0, len(hashes))
	for p := range hashes {
		files = append(files, p)
	}
	return graph.Build(neighbors, files), nil
}

// remoteGraphProjectName resolves the server-side project NAME for the graph
// endpoints. The server keys projects by name, so a path ref cannot be resolved
// remotely; with no --project we only guess when the server has exactly one.
func remoteGraphProjectName(ctx context.Context, d *deps, projectArg string) (string, error) {
	if projectArg != "" {
		return projectArg, nil
	}
	projects, err := d.apiClient().ListProjects(ctx)
	if err != nil {
		return "", err
	}
	if len(projects) == 1 {
		return projects[0].Name, nil
	}
	return "", fmt.Errorf("pass --project <name> (remote mode)")
}
