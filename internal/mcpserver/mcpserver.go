// Package mcpserver exposes a semidx server to AI agents over the Model Context
// Protocol. It is a THIN client: every tool is a call to the HTTP API via
// pkg/client — there is no local database or embedder. Because tools only accept
// project names (never filesystem paths), an agent can never trigger indexing of
// an arbitrary path; only projects already registered on the server are reachable.
package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lgldsilva/semidx/pkg/client"
)

const version = "0.1.0"

// New builds an MCP server whose tools proxy to the given semidx client.
func New(c *client.Client) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "semidx", Version: version}, nil)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "semantic_search",
		Description: "Search a registered project's indexed code semantically with a natural-language query. Returns ranked file:line matches with a content preview. Prefer this over plain grep when the query is about intent or behavior rather than an exact string.",
	}, searchHandler(c))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "semantic_projects",
		Description: "List the projects registered on the semidx server, with their indexing status.",
	}, projectsHandler(c))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "semantic_reindex",
		Description: "Queue a re-index job for a project already registered on the server. Only registered projects can be re-indexed; arbitrary paths are not accepted.",
	}, reindexHandler(c))

	return s
}

// Run serves the MCP protocol over stdio until the client disconnects or ctx is
// cancelled.
func Run(ctx context.Context, c *client.Client) error {
	return New(c).Run(ctx, &mcp.StdioTransport{})
}

type searchInput struct {
	Project string `json:"project" jsonschema:"the registered project name to search"`
	Query   string `json:"query" jsonschema:"the natural-language search query"`
	Model   string `json:"model,omitempty" jsonschema:"optional embedding model override (defaults to the project's model)"`
	TopK    int    `json:"top_k,omitempty" jsonschema:"number of results to return (default 5)"`
}

func searchHandler(c *client.Client) mcp.ToolHandlerFor[searchInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, any, error) {
		topK := in.TopK
		if topK == 0 {
			topK = 5
		}
		resp, err := c.Search(ctx, in.Project, in.Query, in.Model, topK)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(formatSearch(resp)), nil, nil
	}
}

type projectsInput struct{}

func projectsHandler(c *client.Client) mcp.ToolHandlerFor[projectsInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ projectsInput) (*mcp.CallToolResult, any, error) {
		projects, err := c.ListProjects(ctx)
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

func reindexHandler(c *client.Client) mcp.ToolHandlerFor[reindexInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in reindexInput) (*mcp.CallToolResult, any, error) {
		jobType := in.Type
		if jobType == "" {
			jobType = "full"
		}
		id, err := c.EnqueueJob(ctx, in.Project, jobType)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("Queued %s re-index job #%d for project %q.", jobType, id, in.Project)), nil, nil
	}
}

func formatSearch(resp *client.SearchResponse) string {
	if len(resp.Results) == 0 {
		return fmt.Sprintf("No results in project %q for that query.", resp.Project)
	}
	var b strings.Builder
	if resp.Fallback {
		b.WriteString("[warning] embedding was unavailable — results come from keyword search, not semantic ranking.\n\n")
	}
	for i, r := range resp.Results {
		fmt.Fprintf(&b, "%d. %s:%d  (score %.3f)\n%s\n\n", i+1, r.Path, r.StartLine, r.Score, preview(r.Content, 300))
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatProjects(projects []client.Project) string {
	if len(projects) == 0 {
		return "No projects are registered on the server."
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
