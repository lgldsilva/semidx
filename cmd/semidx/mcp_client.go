package main

import (
	"context"
	"log/slog"

	"charm.land/fantasy"

	"github.com/lgldsilva/semidx/internal/mcpclient"
)

// mcpClientTools connects to the external MCP servers configured via
// SEMIDX_MCP_CLIENT_CONFIG and returns their tools as fantasy AgentTools to add
// to the agent's tool set, letting the semidx agent act as a client of other
// MCP servers (e.g. a GitHub MCP).
//
// It is a no-op when unconfigured. Connection/listing failures are logged and
// skipped inside ConnectAll, so a down server never blocks chat. The sessions
// are kept open for the process lifetime (a serve process); stdio children are
// reaped when semidx exits.
func (d *deps) mcpClientTools(ctx context.Context) []fantasy.AgentTool {
	if d.cfg == nil || d.cfg.MCPClientConfig == "" {
		return nil
	}
	cfgs, err := mcpclient.LoadConfig(d.cfg.MCPClientConfig)
	if err != nil {
		slog.Warn("mcp client: could not load server config", "path", d.cfg.MCPClientConfig, "error", err)
		return nil
	}
	tools, _, err := mcpclient.ConnectAll(ctx, cfgs)
	if err != nil {
		slog.Warn("mcp client: no external MCP servers connected", "error", err)
		return nil
	}
	if len(tools) > 0 {
		slog.Info("mcp client: external tools loaded", "tools", len(tools))
	}
	return tools
}
