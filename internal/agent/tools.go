package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lgldsilva/semidx/internal/chat"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
)

// NewSearchTool creates a semantic_search tool for the agent.
func NewSearchTool(svc *search.Service) Tool {
	return &searchTool{svc: svc}
}

type searchTool struct {
	svc *search.Service
}

func (t *searchTool) Def() chat.ToolDef {
	return chat.ToolDef{
		Name:        "semantic_search",
		Description: "Search indexed code and documents by meaning. Returns file:line results with relevance scores. Use for intent/behavior queries, not exact string matching.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Natural language query describing what you're looking for",
				},
				"project": map[string]any{
					"type":        "string",
					"description": "Project name or identity (default: auto-detect from workspace)",
				},
				"top_k": map[string]any{
					"type": "integer", "default": 5,
					"description": "Number of results to return",
				},
			},
			"required": []string{"query"},
		},
	}
}

func (t *searchTool) Run(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Query   string `json:"query"`
		Project string `json:"project,omitempty"`
		TopK    int    `json:"top_k,omitempty"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.TopK <= 0 {
		args.TopK = 5
	}

	resp, err := t.svc.Search(ctx, search.Request{
		Project: args.Project,
		Query:   args.Query,
		TopK:    args.TopK,
	})
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}

	type hit struct {
		File      string  `json:"file"`
		StartLine int     `json:"start_line"`
		EndLine   int     `json:"end_line"`
		Score     float64 `json:"score"`
		Content   string  `json:"content"`
	}
	var hits []hit
	for _, r := range resp.Results {
		hits = append(hits, hit{
			File: r.FilePath, StartLine: r.StartLine, EndLine: r.EndLine,
			Score: r.Score, Content: truncate(r.Content, 200),
		})
	}
	if hits == nil {
		hits = []hit{}
	}
	return JSONResult(map[string]any{
		"results": hits,
		"total":   len(hits),
		"project": resp.Project.Name,
		"keyword": resp.Keyword,
	}), nil
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// NewIndexStatusTool creates an index_status tool.
func NewIndexStatusTool(db store.IndexStore) Tool {
	return &indexStatusTool{db: db}
}

type indexStatusTool struct {
	db store.IndexStore
}

func (t *indexStatusTool) Def() chat.ToolDef {
	return chat.ToolDef{
		Name:        "index_status",
		Description: "Check whether a project is indexed, how many files, and what model. Use before searching if unsure about indexing status.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project name or path (default: current directory)",
				},
			},
			"required": []string{},
		},
	}
}

func (t *indexStatusTool) Run(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Project string `json:"project,omitempty"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	project := args.Project
	if project == "" {
		return JSONResult(map[string]string{
			"error": `no project specified; call semantic_projects first to list available projects`,
		}), nil
	}

	p, err := t.db.GetProject(ctx, project)
	if err != nil {
		p, err = t.db.GetProjectByIdentity(ctx, project)
		if err != nil {
			return "", fmt.Errorf("project %q not found", project)
		}
	}

	hashes, err := t.db.ListFileHashes(ctx, p.ID)
	count := 0
	if err == nil {
		count = len(hashes)
	}

	return JSONResult(map[string]any{
		"name":   p.Name,
		"status": p.Status,
		"model":  p.Model,
		"source": p.SourceType,
		"files":  count,
	}), nil
}
