package mcpclient

import (
	"errors"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestServerConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ServerConfig
		wantErr bool
	}{
		{"valid stdio", ServerConfig{Transport: TransportStdio, Command: "srv"}, false},
		{"valid http", ServerConfig{Transport: TransportHTTP, URL: "http://x"}, false},
		{"stdio without command", ServerConfig{Transport: TransportStdio}, true},
		{"http without url", ServerConfig{Transport: TransportHTTP}, true},
		{"empty transport", ServerConfig{}, true},
		{"unknown transport", ServerConfig{Transport: "carrier-pigeon", Command: "srv"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validate()
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				if !errors.Is(err, errInvalidConfig) {
					t.Errorf("error should wrap errInvalidConfig: %v", err)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestConnectRejectsInvalidConfig(t *testing.T) {
	_, err := Connect(t.Context(), ServerConfig{Transport: "nope"})
	if err == nil {
		t.Fatal("want error for invalid config")
	}
	if !errors.Is(err, errInvalidConfig) {
		t.Errorf("error should wrap errInvalidConfig: %v", err)
	}
}

func TestBuildTransportStdio(t *testing.T) {
	cfg := ServerConfig{
		Transport: TransportStdio,
		Command:   "my-server",
		Args:      []string{"--flag", "value"},
	}
	tr, err := buildTransport(t.Context(), cfg)
	if err != nil {
		t.Fatalf("buildTransport: %v", err)
	}
	cmdT, ok := tr.(*mcp.CommandTransport)
	if !ok {
		t.Fatalf("transport type = %T, want *mcp.CommandTransport", tr)
	}
	if !slices.Contains(cmdT.Command.Args, "--flag") || !slices.Contains(cmdT.Command.Args, "value") {
		t.Errorf("Command.Args = %v, want to contain the configured args", cmdT.Command.Args)
	}
	// With no Env configured, the child inherits the parent environment (Env is
	// left nil).
	if cmdT.Command.Env != nil {
		t.Errorf("Command.Env = %v, want nil (inherit)", cmdT.Command.Env)
	}
}

func TestBuildTransportStdioWithEnv(t *testing.T) {
	cfg := ServerConfig{
		Transport: TransportStdio,
		Command:   "my-server",
		Env:       []string{"SEMIDX_TEST_ENV=1"},
	}
	tr, err := buildTransport(t.Context(), cfg)
	if err != nil {
		t.Fatalf("buildTransport: %v", err)
	}
	cmdT := tr.(*mcp.CommandTransport)
	if !slices.Contains(cmdT.Command.Env, "SEMIDX_TEST_ENV=1") {
		t.Errorf("Command.Env should contain the configured entry: %v", cmdT.Command.Env)
	}
	// Env extends, never replaces, the parent environment.
	if len(cmdT.Command.Env) < 2 {
		t.Errorf("Command.Env should also carry the parent environment, got %d entries", len(cmdT.Command.Env))
	}
}

func TestBuildTransportHTTP(t *testing.T) {
	cfg := ServerConfig{Transport: TransportHTTP, URL: "http://example.test/mcp"}
	tr, err := buildTransport(t.Context(), cfg)
	if err != nil {
		t.Fatalf("buildTransport: %v", err)
	}
	httpT, ok := tr.(*mcp.StreamableClientTransport)
	if !ok {
		t.Fatalf("transport type = %T, want *mcp.StreamableClientTransport", tr)
	}
	if httpT.Endpoint != cfg.URL {
		t.Errorf("Endpoint = %q, want %q", httpT.Endpoint, cfg.URL)
	}
}
