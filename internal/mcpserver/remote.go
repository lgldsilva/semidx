package mcpserver

import (
	"context"
	"fmt"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/pkg/client"
)

// clientBackend adapts the HTTP API client to the Backend interface (remote mode).
type clientBackend struct{ c *client.Client }

// NewClientBackend wraps a semidx API client as an MCP Backend (remote mode).
func NewClientBackend(c *client.Client) Backend { return &clientBackend{c: c} }

func (b *clientBackend) Search(ctx context.Context, project, query, model string, topK int, graph bool, graphDepth int) (*SearchOutput, error) {
	resp, err := b.c.Search(ctx, project, query, client.SearchParams{
		Model: model, TopK: topK, Graph: graph, GraphDepth: graphDepth,
	})
	if err != nil {
		return nil, err
	}
	out := &SearchOutput{Project: resp.Project, Fallback: resp.Fallback}
	for _, r := range resp.Results {
		out.Results = append(out.Results, Hit{Path: r.Path, StartLine: r.StartLine, EndLine: r.EndLine, Score: r.Score, Content: r.Content})
	}
	return out, nil
}

func (b *clientBackend) Projects(ctx context.Context) ([]ProjectInfo, error) {
	projects, err := b.c.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ProjectInfo, 0, len(projects))
	for _, p := range projects {
		out = append(out, ProjectInfo{
			Name: p.Name, SourceType: p.SourceType, GitURL: p.GitURL, Status: p.Status, Model: p.Model,
		})
	}
	return out, nil
}

func (b *clientBackend) Status(ctx context.Context, project string) (*StatusInfo, error) {
	resp, err := b.c.Status(ctx, project)
	if err != nil {
		return nil, err
	}
	return &StatusInfo{
		Name:       resp.Name,
		SourceType: resp.SourceType,
		Identity:   resp.Identity,
		Status:     resp.Status,
		Model:      resp.Model,
		TotalFiles: resp.TotalFiles,
	}, nil
}

func (b *clientBackend) Reindex(ctx context.Context, project, jobType string) (string, error) {
	id, err := b.c.EnqueueJob(ctx, project, jobType)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Queued %s re-index job #%d for project %q.", jobType, id, project), nil
}

func (b *clientBackend) Capabilities() agent.Capabilities {
	return agent.Capabilities{Flags: agent.CapRemoteIndex}
}
