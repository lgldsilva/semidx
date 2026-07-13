package mcpclient

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"charm.land/fantasy"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newFakeHTTPServer starts an httptest server that speaks the streamable HTTP
// MCP protocol backed by buildFakeServer, and returns its URL.
func newFakeHTTPServer(t *testing.T) string {
	t.Helper()
	srv := buildFakeServer()
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestConnectAllAggregatesAndCloses(t *testing.T) {
	url := newFakeHTTPServer(t)
	cfgs := []ServerConfig{
		{Name: "disabled", Transport: TransportStdio, Command: "should-be-skipped", Enabled: false},
		{Name: "http-srv", Transport: TransportHTTP, URL: url, Enabled: true},
	}

	tools, closer, err := ConnectAll(t.Context(), cfgs)
	if err != nil {
		t.Fatalf("ConnectAll: %v", err)
	}
	if closer == nil {
		t.Fatal("closer must never be nil")
	}
	if len(tools) != 3 {
		t.Fatalf("got %d tools, want 3 (echo, boom, ping): %v", len(tools), toolNames(tools))
	}
	if findTool(tools, "mcp__http_srv__echo") == nil {
		t.Errorf("expected namespaced echo tool; got %v", toolNames(tools))
	}

	// The disabled server contributes nothing.
	for _, name := range toolNames(tools) {
		if name == "mcp__disabled__echo" {
			t.Error("disabled server must be skipped")
		}
	}

	// Grab a tool to prove the session is live, then close and prove it's dead.
	ping := findTool(tools, "mcp__http_srv__ping")
	if ping == nil {
		t.Fatal("ping tool not found")
	}
	if resp, err := ping.Run(t.Context(), fantasy.ToolCall{Input: "{}"}); err != nil || resp.IsError {
		t.Fatalf("ping before close should succeed: err=%v resp=%+v", err, resp)
	}

	if err := closer(); err != nil {
		t.Errorf("closer: %v", err)
	}
	if resp, _ := ping.Run(t.Context(), fantasy.ToolCall{Input: "{}"}); !resp.IsError {
		t.Error("after closer(), tool calls should fail")
	}
}

func TestConnectAllNoEnabledServers(t *testing.T) {
	tools, closer, err := ConnectAll(t.Context(), []ServerConfig{
		{Name: "off", Transport: TransportHTTP, URL: "http://x", Enabled: false},
	})
	if err != nil {
		t.Fatalf("ConnectAll: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("want no tools, got %v", toolNames(tools))
	}
	if closer == nil {
		t.Fatal("closer must never be nil")
	}
	if err := closer(); err != nil {
		t.Errorf("closer on empty set: %v", err)
	}
}

func TestConnectAllAllConfigErrors(t *testing.T) {
	// Every enabled server is misconfigured and nothing connects -> ConnectAll
	// returns the joined config errors.
	cfgs := []ServerConfig{
		{Name: "bad-transport", Transport: "bogus", Enabled: true},
		{Name: "stdio-no-cmd", Transport: TransportStdio, Enabled: true},
	}
	tools, closer, err := ConnectAll(t.Context(), cfgs)
	if err == nil {
		t.Fatal("want error when all enabled servers have config errors")
	}
	if !errors.Is(err, errInvalidConfig) {
		t.Errorf("error should wrap errInvalidConfig: %v", err)
	}
	if tools != nil {
		t.Errorf("tools should be nil, got %v", toolNames(tools))
	}
	if closer == nil {
		t.Fatal("closer must never be nil, even on error")
	}
	if err := closer(); err != nil {
		t.Errorf("closer: %v", err)
	}
}

func TestConnectAllRuntimeFailureIgnored(t *testing.T) {
	// A well-formed config whose server can't actually be reached is a runtime
	// failure, not a config error: ConnectAll logs it and returns no error.
	cfgs := []ServerConfig{
		{Name: "dead", Transport: TransportStdio, Command: "semidx-nonexistent-binary-xyz", Enabled: true},
	}
	tools, closer, err := ConnectAll(t.Context(), cfgs)
	if err != nil {
		t.Fatalf("runtime failure must not be returned as error: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("want no tools, got %v", toolNames(tools))
	}
	if closer == nil {
		t.Fatal("closer must never be nil")
	}
	if err := closer(); err != nil {
		t.Errorf("closer: %v", err)
	}
}

func TestConnectAllPartialSuccess(t *testing.T) {
	// One good server + one broken-config server: since at least one connected,
	// ConnectAll returns its tools and no error (the bad one is only logged).
	url := newFakeHTTPServer(t)
	cfgs := []ServerConfig{
		{Name: "broken", Transport: "bogus", Enabled: true},
		{Name: "good", Transport: TransportHTTP, URL: url, Enabled: true},
	}
	tools, closer, err := ConnectAll(t.Context(), cfgs)
	if err != nil {
		t.Fatalf("partial success must not error: %v", err)
	}
	t.Cleanup(func() { _ = closer() })
	if len(tools) != 3 {
		t.Errorf("want 3 tools from the good server, got %v", toolNames(tools))
	}
}
