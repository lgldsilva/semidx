// Package mcpclient lets the semidx agent act as a *client* of external MCP
// servers. It connects to a server (over stdio or streamable HTTP), lists the
// server's tools, and wraps each one as a charm.land/fantasy AgentTool so the
// remote tools can be merged into the agent's own tool set.
//
// The package is deliberately self-contained: it knows how to connect, list and
// call remote tools, but it does not know how the resulting tools are wired into
// the agent runner or where the server configuration comes from — that is the
// caller's job.
package mcpclient

import (
	"errors"
	"fmt"
)

// Transport kinds understood by [Connect].
const (
	// TransportStdio launches Command with Args (and optional Env) and speaks
	// MCP over the child process's stdin/stdout.
	TransportStdio = "stdio"
	// TransportHTTP connects to a streamable HTTP MCP endpoint at URL.
	TransportHTTP = "http"
)

// ServerConfig describes a single external MCP server to connect to.
//
// Exactly one transport is used, selected by Transport:
//   - "stdio": Command is required; Args and Env are optional.
//   - "http": URL is required.
//
// Env entries are "KEY=VALUE" strings appended to the current process
// environment for the child (stdio only); they never replace it.
type ServerConfig struct {
	// Name identifies the server. It is used to namespace the server's tools
	// (see [Session.Tools]) and in log messages. It should be unique across the
	// configured servers.
	Name string
	// Transport selects the connection kind: [TransportStdio] or [TransportHTTP].
	Transport string
	// Command is the executable to launch (stdio transport).
	Command string
	// Args are the arguments passed to Command (stdio transport).
	Args []string
	// Env holds extra "KEY=VALUE" environment entries for the child process
	// (stdio transport). They are appended to the parent environment.
	Env []string
	// URL is the streamable HTTP endpoint (http transport).
	URL string
	// Enabled reports whether the server should be connected. Disabled servers
	// are skipped by [ConnectAll].
	Enabled bool
}

// errInvalidConfig is the sentinel wrapped by every configuration validation
// failure, so callers (notably [ConnectAll]) can tell a bad configuration apart
// from a runtime connection failure with [errors.Is].
var errInvalidConfig = errors.New("invalid mcp server config")

// validate reports whether the configuration is usable. It checks that the
// transport is known and that the transport-specific required fields are set.
func (c ServerConfig) validate() error {
	switch c.Transport {
	case TransportStdio:
		if c.Command == "" {
			return fmt.Errorf("%w: stdio transport requires a command", errInvalidConfig)
		}
	case TransportHTTP:
		if c.URL == "" {
			return fmt.Errorf("%w: http transport requires a url", errInvalidConfig)
		}
	case "":
		return fmt.Errorf("%w: transport is empty (want %q or %q)", errInvalidConfig, TransportStdio, TransportHTTP)
	default:
		return fmt.Errorf("%w: unknown transport %q (want %q or %q)", errInvalidConfig, c.Transport, TransportStdio, TransportHTTP)
	}
	return nil
}
