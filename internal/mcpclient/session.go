package mcpclient

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"charm.land/fantasy"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// clientName and clientVersion identify semidx to the remote MCP server during
// the initialize handshake. They are informational only.
const (
	clientName    = "semidx"
	clientVersion = "dev"
)

// Session is a live connection to one external MCP server. It wraps the
// underlying *mcp.ClientSession and the configured server name (used to
// namespace tools). It is safe to call Tools multiple times; call Close when
// done.
type Session struct {
	name string
	cs   *mcp.ClientSession
}

// newSession wraps an already-connected *mcp.ClientSession. It exists so the
// session/tool/run logic can be exercised with an in-memory transport in tests
// without going through [Connect]'s transport construction.
func newSession(name string, cs *mcp.ClientSession) *Session {
	return &Session{name: name, cs: cs}
}

// Name returns the configured server name.
func (s *Session) Name() string { return s.name }

// Connect validates cfg, builds the appropriate transport, and connects a new
// MCP client session to the server. The returned Session must be closed with
// [Session.Close].
//
// ctx governs the connection and, for the stdio transport, the lifetime of the
// launched child process.
func Connect(ctx context.Context, cfg ServerConfig) (*Session, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	transport, err := buildTransport(ctx, cfg)
	if err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: clientName, Version: clientVersion}, nil)
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to mcp server %q: %w", cfg.Name, err)
	}
	return newSession(cfg.Name, cs), nil
}

// buildTransport constructs the mcp.Transport described by cfg. cfg is assumed
// to already be valid (see [ServerConfig.validate]).
func buildTransport(ctx context.Context, cfg ServerConfig) (mcp.Transport, error) {
	switch cfg.Transport {
	case TransportStdio:
		// #nosec G204 -- Command/Args come from the operator's MCP server config
		// (SEMIDX_MCP_CLIENT_CONFIG), not from end-user/request input; launching
		// the configured server is the whole point of the stdio transport.
		cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
		if len(cfg.Env) > 0 {
			// Extend, never replace, the parent environment so the child keeps
			// PATH and friends.
			cmd.Env = append(os.Environ(), cfg.Env...)
		}
		return &mcp.CommandTransport{Command: cmd}, nil
	case TransportHTTP:
		return &mcp.StreamableClientTransport{Endpoint: cfg.URL}, nil
	default:
		// Unreachable after validate, but keep the switch total.
		return nil, fmt.Errorf("%w: unknown transport %q", errInvalidConfig, cfg.Transport)
	}
}

// Tools lists the server's tools and wraps each one as a fantasy.AgentTool.
//
// To avoid colliding with semidx's native tool names, the name exposed to the
// model is namespaced as "mcp__<server>__<toolname>" (the server segment is
// sanitized to [A-Za-z0-9_]); the wrapper still calls the tool by its original
// name on the wire.
func (s *Session) Tools(ctx context.Context) ([]fantasy.AgentTool, error) {
	res, err := s.cs.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list tools from mcp server %q: %w", s.name, err)
	}
	server := sanitizeServerName(s.name)
	tools := make([]fantasy.AgentTool, 0, len(res.Tools))
	for _, t := range res.Tools {
		if t == nil {
			continue
		}
		params, required := parseSchema(t.InputSchema)
		tools = append(tools, &remoteTool{
			session:     s,
			rawName:     t.Name,
			name:        fmt.Sprintf("mcp__%s__%s", server, t.Name),
			description: t.Description,
			parameters:  params,
			required:    required,
		})
	}
	return tools, nil
}

// Close terminates the session. For the stdio transport this also shuts the
// child process down.
func (s *Session) Close() error {
	return s.cs.Close()
}

// sanitizeServerName replaces every character outside [A-Za-z0-9_] with '_' so
// the namespaced tool name stays a safe identifier.
func sanitizeServerName(name string) string {
	out := []rune(name)
	for i, r := range out {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_':
			// keep
		default:
			out[i] = '_'
		}
	}
	return string(out)
}
