package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/internal/usage"
	"github.com/lgldsilva/semidx/pkg/client"
)

// UsageBackend is an optional Backend capability for semantic_usage.
type UsageBackend interface {
	Usage(ctx context.Context, days int, project string) (usage.Report, error)
}

type usageArgs struct {
	Days    int    `json:"days,omitempty" jsonschema:"lookback window in days (default 30, max 365)"`
	Project string `json:"project,omitempty" jsonschema:"optional project name filter"`
}

func usageHandler(b UsageBackend) mcp.ToolHandlerFor[usageArgs, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args usageArgs) (*mcp.CallToolResult, any, error) {
		days := args.Days
		if days <= 0 {
			days = 30
		}
		if days > 365 {
			return errorResult(fmt.Errorf("days must be 1..365")), nil, nil
		}
		report, err := b.Usage(ctx, days, args.Project)
		if err != nil {
			return errorResult(err), nil, nil
		}
		data, _ := json.MarshalIndent(report, "", "  ")
		return textResult(string(data)), nil, nil
	}
}

// localUsage adapts a store.UsageStore for MCP.
type localUsage struct {
	store store.UsageStore
}

func (l localUsage) Usage(ctx context.Context, days int, project string) (usage.Report, error) {
	since := time.Now().UTC().AddDate(0, 0, -days)
	agg, err := l.store.UsageAggregate(ctx, since, project, 10)
	if err != nil {
		return usage.Report{}, err
	}
	return usage.BuildReport(agg, usage.Params{SinceDays: days, TopLimit: 10, Project: project}, time.Now().UTC()), nil
}

// remoteUsage adapts the HTTP client for MCP.
type remoteUsage struct {
	c *client.Client
}

func (r remoteUsage) Usage(ctx context.Context, days int, project string) (usage.Report, error) {
	raw, err := r.c.SearchUsage(ctx, days, project)
	if err != nil {
		return usage.Report{}, err
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return usage.Report{}, err
	}
	var report usage.Report
	if err := json.Unmarshal(b, &report); err != nil {
		return usage.Report{}, err
	}
	return report, nil
}

// WithUsage wraps a Backend so New also registers semantic_usage.
// Call after NewClientBackend / NewLocalBackend.
func WithUsage(b Backend, us store.UsageStore) Backend {
	if us == nil {
		return b
	}
	return &usageAwareBackend{Backend: b, UsageBackend: localUsage{store: us}}
}

// WithRemoteUsage attaches usage reporting to a remote backend.
func WithRemoteUsage(b Backend, c *client.Client) Backend {
	if c == nil {
		return b
	}
	return &usageAwareBackend{Backend: b, UsageBackend: remoteUsage{c: c}}
}

type usageAwareBackend struct {
	Backend
	UsageBackend
}

// Unwrap exposes the wrapped Backend so asGitBackend/asMultiSearchBackend/
// asGraphBackend can see capabilities of the backend WithUsage/WithRemoteUsage
// wraps, regardless of wrap order.
func (b *usageAwareBackend) Unwrap() Backend { return b.Backend }
