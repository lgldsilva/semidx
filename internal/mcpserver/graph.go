package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lgldsilva/semidx/internal/search"
)

type neighborsInput struct {
	Project string `json:"project,omitempty" jsonschema:"the registered project name (optional when a default project is configured)"`
	File    string `json:"file" jsonschema:"the relative path of the file to query neighbors for"`
}

type traceInput struct {
	Project  string   `json:"project,omitempty" jsonschema:"the registered project name (optional when a default project is configured)"`
	Files    []string `json:"files" jsonschema:"the relative paths of the files to start the trace from"`
	MaxDepth int      `json:"max_depth,omitempty" jsonschema:"maximum BFS depth for trace expansion (default 2, max 5)"`
}

type symbolsInput struct {
	Project string `json:"project,omitempty" jsonschema:"the registered project name (optional when a default project is configured)"`
	File    string `json:"file" jsonschema:"the relative path of the file to extract symbols from"`
}

// registerGraphTools registers the graph tools when the backend (or a backend it
// wraps) implements GraphBackend.
func registerGraphTools(s *mcp.Server, b Backend, allowed map[string]bool, explicit bool, defaultProject string) {
	graphB, ok := asGraphBackend(b)
	if !ok {
		if explicit {
			warnUnavailable(allowed, "a local graph backend", toolSemanticNeighbors, toolSemanticTrace, toolSemanticSymbols)
		}
		return
	}
	if allowed[toolSemanticNeighbors] {
		mcp.AddTool(s, &mcp.Tool{
			Name:        toolSemanticNeighbors,
			Description: projectToolDescription("Get the import/export neighbors of a file in the dependency graph.", defaultProject),
			Annotations: readOnlyAnnotations("Dependency neighbors"),
		}, neighborsHandler(graphB, b, defaultProject))
	}
	if allowed[toolSemanticTrace] {
		mcp.AddTool(s, &mcp.Tool{
			Name:        toolSemanticTrace,
			Description: projectToolDescription("Trace dependency paths starting from seed files up to a maximum depth.", defaultProject),
			Annotations: readOnlyAnnotations("Dependency trace"),
		}, traceHandler(graphB, b, defaultProject))
	}
	if allowed[toolSemanticSymbols] {
		mcp.AddTool(s, &mcp.Tool{
			Name:        toolSemanticSymbols,
			Description: projectToolDescription("List all symbols defined in a file.", defaultProject),
			Annotations: readOnlyAnnotations("File symbols"),
		}, symbolsHandler(graphB, b, defaultProject))
	}
}

func neighborsHandler(gb GraphBackend, b Backend, defaultProject string) mcp.ToolHandlerFor[neighborsInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in neighborsInput) (*mcp.CallToolResult, any, error) {
		project, err := resolveProjectForTool(ctx, b, in.Project, defaultProject)
		if err != nil {
			return errorResult(err), nil, nil
		}
		res, err := gb.Neighbors(ctx, project, in.File)
		return jsonToolResult(res, err)
	}
}

func traceHandler(gb GraphBackend, b Backend, defaultProject string) mcp.ToolHandlerFor[traceInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in traceInput) (*mcp.CallToolResult, any, error) {
		project, err := resolveProjectForTool(ctx, b, in.Project, defaultProject)
		if err != nil {
			return errorResult(err), nil, nil
		}
		depth := search.ClampGraphDepth(in.MaxDepth)
		res, err := gb.Trace(ctx, project, in.Files, depth)
		return jsonToolResult(res, err)
	}
}

func symbolsHandler(gb GraphBackend, b Backend, defaultProject string) mcp.ToolHandlerFor[symbolsInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in symbolsInput) (*mcp.CallToolResult, any, error) {
		project, err := resolveProjectForTool(ctx, b, in.Project, defaultProject)
		if err != nil {
			return errorResult(err), nil, nil
		}
		res, err := gb.Symbols(ctx, project, in.File)
		return jsonToolResult(res, err)
	}
}
