package mcpclient

import (
	"context"
	"encoding/json"
	"testing"

	"charm.land/fantasy"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// buildFakeServer returns an MCP server exposing three tools used across the
// tests:
//   - "echo": has an input schema (properties message+times, message required)
//     and echoes the received arguments back as JSON text.
//   - "boom": no meaningful schema; always returns an IsError result.
//   - "ping": no schema; ignores input and returns "pong".
func buildFakeServer() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "0"}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "echo",
		Description: "echoes arguments back",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{"type": "string", "description": "text to echo"},
				"times":   map[string]any{"type": "integer"},
			},
			"required": []string{"message"},
		},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		raw, _ := json.Marshal(in)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "boom",
		Description: "always errors",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: "kaboom"}},
		}, nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "ping",
		Description: "returns pong",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil, nil
	})

	return srv
}

// newFakeSession connects the fake server to an in-memory transport and returns
// a Session wrapping the client end. The server name intentionally contains a
// character outside [A-Za-z0-9_] so tests exercise name sanitization.
func newFakeSession(t *testing.T) *Session {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	srv := buildFakeServer()
	serverT, clientT := mcp.NewInMemoryTransports()

	// The server must be connected before the client (the client drives the
	// initialize handshake).
	serverSession, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	sess := newSession("fake-srv", cs)
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func findTool(tools []fantasy.AgentTool, name string) fantasy.AgentTool {
	for _, tool := range tools {
		if tool.Info().Name == name {
			return tool
		}
	}
	return nil
}

func TestSessionName(t *testing.T) {
	sess := newFakeSession(t)
	if sess.Name() != "fake-srv" {
		t.Errorf("Name() = %q, want %q", sess.Name(), "fake-srv")
	}
}

func TestSessionToolsPrefixAndSchema(t *testing.T) {
	sess := newFakeSession(t)
	tools, err := sess.Tools(t.Context())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 3 {
		t.Fatalf("got %d tools, want 3", len(tools))
	}

	// Names are namespaced as mcp__<sanitized-server>__<toolname>. "fake-srv"
	// sanitizes to "fake_srv".
	echo := findTool(tools, "mcp__fake_srv__echo")
	if echo == nil {
		t.Fatalf("echo tool not found; names: %v", toolNames(tools))
	}
	if findTool(tools, "mcp__fake_srv__boom") == nil {
		t.Errorf("boom tool not found; names: %v", toolNames(tools))
	}

	info := echo.Info()
	if info.Description != "echoes arguments back" {
		t.Errorf("description = %q", info.Description)
	}
	if info.Parallel {
		t.Error("remote tools must not be marked Parallel")
	}
	if _, ok := info.Parameters["message"]; !ok {
		t.Errorf("Parameters missing 'message': %v", info.Parameters)
	}
	if _, ok := info.Parameters["times"]; !ok {
		t.Errorf("Parameters missing 'times': %v", info.Parameters)
	}
	if len(info.Required) != 1 || info.Required[0] != "message" {
		t.Errorf("Required = %v, want [message]", info.Required)
	}

	// A tool without a meaningful schema yields empty, non-nil Parameters and
	// Required.
	boom := findTool(tools, "mcp__fake_srv__boom")
	binfo := boom.Info()
	if binfo.Parameters == nil {
		t.Error("Parameters must be non-nil")
	}
	if len(binfo.Parameters) != 0 {
		t.Errorf("Parameters = %v, want empty", binfo.Parameters)
	}
	if binfo.Required == nil {
		t.Error("Required must be non-nil")
	}
	if len(binfo.Required) != 0 {
		t.Errorf("Required = %v, want empty", binfo.Required)
	}
}

func TestRemoteToolRunRoundTrip(t *testing.T) {
	sess := newFakeSession(t)
	tools, err := sess.Tools(t.Context())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	echo := findTool(tools, "mcp__fake_srv__echo")
	if echo == nil {
		t.Fatal("echo tool not found")
	}

	resp, err := echo.Run(t.Context(), fantasy.ToolCall{Input: `{"message":"hi","times":2}`})
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if resp.IsError {
		t.Fatalf("unexpected error response: %s", resp.Content)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(resp.Content), &got); err != nil {
		t.Fatalf("content not JSON: %q (%v)", resp.Content, err)
	}
	if got["message"] != "hi" {
		t.Errorf("round-trip message = %v, want hi (full: %v)", got["message"], got)
	}
}

func TestRemoteToolRunEmptyInput(t *testing.T) {
	sess := newFakeSession(t)
	tools, err := sess.Tools(t.Context())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	ping := findTool(tools, "mcp__fake_srv__ping")
	if ping == nil {
		t.Fatal("ping tool not found")
	}

	// Empty input must be forwarded as no arguments (not an invalid "" body).
	resp, err := ping.Run(t.Context(), fantasy.ToolCall{Input: "   "})
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if resp.IsError {
		t.Fatalf("unexpected error response: %s", resp.Content)
	}
	if resp.Content != "pong" {
		t.Errorf("content = %q, want pong", resp.Content)
	}
}

func TestRemoteToolRunIsError(t *testing.T) {
	sess := newFakeSession(t)
	tools, err := sess.Tools(t.Context())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	boom := findTool(tools, "mcp__fake_srv__boom")
	if boom == nil {
		t.Fatal("boom tool not found")
	}

	resp, err := boom.Run(t.Context(), fantasy.ToolCall{Input: "{}"})
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if !resp.IsError {
		t.Errorf("expected IsError response, got: %s", resp.Content)
	}
	if resp.Content != "kaboom" {
		t.Errorf("content = %q, want kaboom", resp.Content)
	}
}

func TestRemoteToolRunTransportError(t *testing.T) {
	sess := newFakeSession(t)
	tools, err := sess.Tools(t.Context())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	ping := findTool(tools, "mcp__fake_srv__ping")
	if ping == nil {
		t.Fatal("ping tool not found")
	}

	// Closing the session makes the next call fail at the transport level. It
	// must surface as an error *response*, not a returned error, so the agent
	// loop keeps going.
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	resp, err := ping.Run(t.Context(), fantasy.ToolCall{Input: "{}"})
	if err != nil {
		t.Fatalf("Run must not return a Go error on transport failure, got: %v", err)
	}
	if !resp.IsError {
		t.Error("transport failure must produce an IsError response")
	}
	if resp.Content == "" {
		t.Error("transport failure response should carry the error text")
	}
}

func toolNames(tools []fantasy.AgentTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Info().Name)
	}
	return names
}

func TestSessionToolsListError(t *testing.T) {
	// Listing tools on an already-closed session surfaces an error.
	sess := newFakeSession(t)
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := sess.Tools(t.Context()); err == nil {
		t.Error("Tools on a closed session should error")
	}
}
