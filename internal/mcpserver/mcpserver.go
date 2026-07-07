// Package mcpserver exposes semidx to AI agents over the Model Context Protocol.
// It is backend-agnostic: the three tools call a Backend, which is either a THIN
// client over the HTTP API (remote mode, pkg/client) or a local adapter over the
// standalone index (local mode, search.Service + the local store). Either way the
// tools only accept project NAMES (never filesystem paths), so an agent can never
// trigger indexing of an arbitrary path — only already-registered/indexed projects
// are reachable.
package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const version = "0.1.0"

// Hit is one ranked search result, independent of the backend that produced it.
type Hit struct {
	Path      string
	StartLine int
	Score     float64
	Content   string
}

// SearchOutput is a backend-neutral search result set.
type SearchOutput struct {
	Project  string
	Fallback bool
	Results  []Hit
}

// ProjectInfo is a backend-neutral project summary.
type ProjectInfo struct {
	Name       string
	SourceType string
	GitURL     string
	Status     string
	Model      string
}

// StatusInfo is a backend-neutral project status summary.
type StatusInfo struct {
	Name       string
	SourceType string
	Identity   string
	Status     string
	Model      string
	TotalFiles int
}

// Backend is the data source the MCP tools call. Implemented by the remote HTTP
// client (NewClientBackend) and the local index (NewLocalBackend).
type Backend interface {
	Search(ctx context.Context, project, query, model string, topK int, graph bool, graphDepth int) (*SearchOutput, error)
	Projects(ctx context.Context) ([]ProjectInfo, error)
	// Reindex returns a human-readable status message on success.
	Reindex(ctx context.Context, project, jobType string) (string, error)
	// Status returns the indexing status of a registered project.
	Status(ctx context.Context, project string) (*StatusInfo, error)
}

// New builds an MCP server whose tools call the given backend.
func New(b Backend) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "semidx", Version: version}, nil)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "semantic_search",
		Description: "Search a registered project's indexed code semantically with a natural-language query. Returns ranked file:line matches with a content preview. Prefer this over plain grep when the query is about intent or behavior rather than an exact string.",
	}, searchHandler(b))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "semantic_projects",
		Description: "List the projects registered in this semidx index, with their indexing status.",
	}, projectsHandler(b))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "semantic_reindex",
		Description: "Queue a re-index job for a project already registered on the server. Only registered projects can be re-indexed; arbitrary paths are not accepted. In standalone (local) mode, reindex via the `semidx index` CLI instead.",
	}, reindexHandler(b))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "semantic_status",
		Description: "Get the indexing status of a registered project. Reports file count, status, and model.",
	}, statusHandler(b))

	// Conditionally register semantic_ask if the backend supports RAG.
	if ab, ok := b.(AskBackend); ok {
		mcp.AddTool(s, &mcp.Tool{
			Name:        "semantic_ask",
			Description: "Ask a question about a registered project using RAG (Retrieval-Augmented Generation). Searches the index for relevant chunks, then uses an LLM to compose an answer citing file:line sources. Stateless — each call is independent.",
		}, askHandler(ab))
	}

	return s
}

// Run serves the MCP protocol over stdio until the client disconnects or ctx is
// cancelled.
func Run(ctx context.Context, b Backend) error {
	return New(b).Run(ctx, &mcp.StdioTransport{})
}

type searchInput struct {
	Project    string `json:"project" jsonschema:"the registered project name to search"`
	Query      string `json:"query" jsonschema:"the natural-language search query"`
	Model      string `json:"model,omitempty" jsonschema:"optional embedding model override (defaults to the project's model)"`
	TopK       int    `json:"top_k,omitempty" jsonschema:"number of results to return (default 5)"`
	Graph      bool   `json:"graph,omitempty" jsonschema:"expand results via dependency graph (Graph-RAG)"`
	GraphDepth int    `json:"graph_depth,omitempty" jsonschema:"max BFS depth for graph expansion (default 2)"`
}

func searchHandler(b Backend) mcp.ToolHandlerFor[searchInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, any, error) {
		topK := in.TopK
		if topK == 0 {
			topK = 5
		}
		graphDepth := in.GraphDepth
		if graphDepth == 0 {
			graphDepth = 2
		}
		out, err := b.Search(ctx, in.Project, in.Query, in.Model, topK, in.Graph, graphDepth)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(formatSearch(out)), nil, nil
	}
}

type projectsInput struct{}

func projectsHandler(b Backend) mcp.ToolHandlerFor[projectsInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ projectsInput) (*mcp.CallToolResult, any, error) {
		projects, err := b.Projects(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(formatProjects(projects)), nil, nil
	}
}

type reindexInput struct {
	Project string `json:"project" jsonschema:"the registered project to re-index"`
	Type    string `json:"type,omitempty" jsonschema:"job type: full or git_history (default full)"`
}

func reindexHandler(b Backend) mcp.ToolHandlerFor[reindexInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in reindexInput) (*mcp.CallToolResult, any, error) {
		jobType := in.Type
		if jobType == "" {
			jobType = "full"
		}
		msg, err := b.Reindex(ctx, in.Project, jobType)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(msg), nil, nil
	}
}

type statusInput struct {
	Project string `json:"project" jsonschema:"the registered project name to check status for"`
}

func statusHandler(b Backend) mcp.ToolHandlerFor[statusInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in statusInput) (*mcp.CallToolResult, any, error) {
		info, err := b.Status(ctx, in.Project)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(formatStatus(info)), nil, nil
	}
}

func formatStatus(info *StatusInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Project: %s\n", info.Name)
	if info.Identity != "" {
		fmt.Fprintf(&b, "Identity: %s\n", info.Identity)
	}
	fmt.Fprintf(&b, "Source: %s\n", info.SourceType)
	fmt.Fprintf(&b, "Status: %s\n", info.Status)
	if info.Model != "" {
		fmt.Fprintf(&b, "Model: %s\n", info.Model)
	}
	fmt.Fprintf(&b, "Total indexed: %d files\n", info.TotalFiles)
	fmt.Fprintln(&b)
	fmt.Fprint(&b, "Tip: projects may have unindexed changes. Use semantic_reindex (server mode) or `semidx index` (standalone) to refresh.")
	return strings.TrimRight(b.String(), "\n")
}

func formatSearch(out *SearchOutput) string {
	if len(out.Results) == 0 {
		return fmt.Sprintf("No results in project %q for that query.", out.Project)
	}
	var b strings.Builder
	if out.Fallback {
		b.WriteString("[warning] embedding was unavailable — results come from keyword search, not semantic ranking.\n\n")
	}
	for i, r := range out.Results {
		fmt.Fprintf(&b, "%d. %s:%d  (score %.3f)\n%s\n\n", i+1, r.Path, r.StartLine, r.Score, preview(r.Content, 300))
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatProjects(projects []ProjectInfo) string {
	if len(projects) == 0 {
		return "No projects are registered in this index."
	}
	var b strings.Builder
	for _, p := range projects {
		src := p.SourceType
		if p.GitURL != "" {
			src = fmt.Sprintf("%s (%s)", p.SourceType, p.GitURL)
		}
		fmt.Fprintf(&b, "- %s  [%s]  status=%s  model=%s\n", p.Name, src, p.Status, p.Model)
	}
	return strings.TrimRight(b.String(), "\n")
}

func preview(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// errorResult surfaces a business error to the agent as tool content with
// IsError set — an in-band error the model can read and react to, not a
// protocol-level failure.
func errorResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}
}
