package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lgldsilva/semidx/internal/graph"
	"github.com/lgldsilva/semidx/pkg/client"
)

// DepGraphBackend extends Backend with the walkable file↔package dependency
// graph: an ego subgraph around a seed file, and a shortest path between two
// files. Distinct from GraphBackend (semantic_neighbors/trace/symbols), which
// exposes the raw adjacency map; this one returns node/edge documents with
// package hops, and is implemented by BOTH the local and the remote backend.
type DepGraphBackend interface {
	Backend
	GraphSubgraph(ctx context.Context, project, seed string, depth, limit int) (*client.GraphSubgraphResponse, error)
	GraphPath(ctx context.Context, project, from, to string, maxDepth int, undirected bool) (*client.GraphPathResponse, error)
}

// asDepGraphBackend finds a DepGraphBackend in b or its wrapped chain, so an
// ask/RAG wrapper cannot hide the dependency-graph tools.
func asDepGraphBackend(b Backend) (DepGraphBackend, bool) {
	for b != nil {
		if db, ok := b.(DepGraphBackend); ok {
			return db, true
		}
		u, ok := b.(unwrapper)
		if !ok {
			return nil, false
		}
		b = u.Unwrap()
	}
	return nil, false
}

type subgraphInput struct {
	Project string `json:"project,omitempty" jsonschema:"the registered project name (optional when a default project is configured)"`
	File    string `json:"file,omitempty" jsonschema:"project-relative path of the seed file; omit for a hub sample of the whole project"`
	Depth   int    `json:"depth,omitempty" jsonschema:"max BFS depth (default 2, max 5)"`
	Limit   int    `json:"limit,omitempty" jsonschema:"max edges returned (default 500)"`
}

type depPathInput struct {
	Project    string `json:"project,omitempty" jsonschema:"the registered project name (optional when a default project is configured)"`
	From       string `json:"from" jsonschema:"source file (project-relative)"`
	To         string `json:"to" jsonschema:"target file (project-relative)"`
	MaxDepth   int    `json:"max_depth,omitempty" jsonschema:"max BFS depth (default 8, max 16)"`
	Undirected bool   `json:"undirected,omitempty" jsonschema:"allow reverse hops; the result then reports directed=false"`
}

// registerDepGraphTools registers semantic_subgraph and semantic_path when the
// backend (or one it wraps) implements DepGraphBackend.
func registerDepGraphTools(s *mcp.Server, b Backend, allowed map[string]bool, explicit bool, defaultProject string) {
	dg, ok := asDepGraphBackend(b)
	if !ok {
		if explicit {
			warnUnavailable(allowed, "a dependency-graph-capable backend", toolSemanticSubgraph, toolSemanticPath)
		}
		return
	}
	if allowed[toolSemanticSubgraph] {
		addProjectTool(s, &mcp.Tool{
			Name: toolSemanticSubgraph,
			Description: projectToolDescription(
				"Return the file↔package dependency neighborhood around a file as nodes and edges (imports plus package membership). Use to see what a file structurally connects to; omit \"file\" for a hub sample of the project.",
				defaultProject,
			),
		}, defaultProject, subgraphHandler(dg, defaultProject))
	}
	if allowed[toolSemanticPath] {
		addProjectTool(s, &mcp.Tool{
			Name: toolSemanticPath,
			Description: projectToolDescription(
				"Find how file A communicates with file B via the shortest dependency path (import edges plus package hops). Set undirected=true to allow reverse hops; the result then reports directed=false.",
				defaultProject,
			),
		}, defaultProject, depPathHandler(dg, defaultProject))
	}
}

func subgraphHandler(b DepGraphBackend, defaultProject string) mcp.ToolHandlerFor[subgraphInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in subgraphInput) (*mcp.CallToolResult, any, error) {
		project, err := requireResolvedProject(in.Project, defaultProject)
		if err != nil {
			return errorResult(err), nil, nil
		}
		res, err := b.GraphSubgraph(ctx, project, in.File, in.Depth, in.Limit)
		return jsonToolResult(res, err)
	}
}

func depPathHandler(b DepGraphBackend, defaultProject string) mcp.ToolHandlerFor[depPathInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in depPathInput) (*mcp.CallToolResult, any, error) {
		project, err := requireResolvedProject(in.Project, defaultProject)
		if err != nil {
			return errorResult(err), nil, nil
		}
		if in.From == "" || in.To == "" {
			return errorResult(fmt.Errorf("from and to are required")), nil, nil
		}
		res, err := b.GraphPath(ctx, project, in.From, in.To, in.MaxDepth, in.Undirected)
		return jsonToolResult(res, err)
	}
}

// --- local backend ------------------------------------------------------------

func (b *localBackend) GraphSubgraph(ctx context.Context, project, seed string, depth, limit int) (*client.GraphSubgraphResponse, error) {
	if seed != "" {
		if err := validateRelativePath(seed); err != nil {
			return nil, fmt.Errorf("invalid file path: %w", err)
		}
	}
	idx, err := b.loadDepGraphIndex(ctx, project)
	if err != nil {
		return nil, err
	}
	sg := idx.Subgraph(seed, graph.Budget{MaxDepth: depth, MaxEdgesOut: limit})
	return &client.GraphSubgraphResponse{
		Nodes:     toClientGraphNodes(sg.Nodes),
		Edges:     toClientGraphEdges(sg.Edges),
		Truncated: sg.Truncated,
	}, nil
}

func (b *localBackend) GraphPath(ctx context.Context, project, from, to string, maxDepth int, undirected bool) (*client.GraphPathResponse, error) {
	for _, p := range []string{from, to} {
		if err := validateRelativePath(p); err != nil {
			return nil, fmt.Errorf("invalid file path %q: %w", p, err)
		}
	}
	idx, err := b.loadDepGraphIndex(ctx, project)
	if err != nil {
		return nil, err
	}
	pr := idx.ShortestPath(from, to, graph.Budget{MaxDepth: maxDepth}, undirected)
	return &client.GraphPathResponse{
		From: pr.From, To: pr.To, Found: pr.Found, Directed: pr.Directed,
		Hops: pr.Hops, Edges: toClientGraphEdges(pr.Edges),
		Length: pr.Length, Truncated: pr.Truncated,
	}, nil
}

// loadDepGraphIndex builds the walkable index for a project from the store's
// adjacency map plus the full file inventory — the inventory is what makes
// package→file hops visible for files that import nothing.
func (b *localBackend) loadDepGraphIndex(ctx context.Context, project string) (*graph.Index, error) {
	p, gl, err := b.graphProject(ctx, project)
	if err != nil {
		return nil, err
	}
	neighbors, err := gl.FetchGraphNeighbors(ctx, p.ID)
	if err != nil {
		return nil, fmt.Errorf("could not load dependency graph")
	}
	hashes, err := b.idx.ListFileHashes(ctx, p.ID)
	if err != nil {
		return nil, fmt.Errorf("could not list project files")
	}
	files := make([]string, 0, len(hashes))
	for path := range hashes {
		files = append(files, path)
	}
	return graph.Build(neighbors, files), nil
}

func toClientGraphNodes(in []graph.Node) []client.GraphNode {
	out := make([]client.GraphNode, len(in))
	for i, n := range in {
		out[i] = client.GraphNode{ID: n.ID, Label: n.Label, Kind: n.Kind, Seed: n.Seed}
	}
	return out
}

func toClientGraphEdges(in []graph.Edge) []client.GraphEdge {
	out := make([]client.GraphEdge, len(in))
	for i, e := range in {
		out[i] = client.GraphEdge{Source: e.Source, Target: e.Target, Kind: e.Kind, Reverse: e.Reverse}
	}
	return out
}

// --- remote backend -----------------------------------------------------------

func (b *clientBackend) GraphSubgraph(ctx context.Context, project, seed string, depth, limit int) (*client.GraphSubgraphResponse, error) {
	return b.c.GraphSubgraph(ctx, project, seed, depth, limit)
}

func (b *clientBackend) GraphPath(ctx context.Context, project, from, to string, maxDepth int, undirected bool) (*client.GraphPathResponse, error) {
	return b.c.GraphPath(ctx, project, from, to, maxDepth, undirected)
}
