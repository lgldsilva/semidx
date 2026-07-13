package mcpclient

import (
	"strings"
	"testing"

	"charm.land/fantasy"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSanitizeServerName(t *testing.T) {
	tests := map[string]string{
		"fake-srv":      "fake_srv",
		"my server!":    "my_server_",
		"ok_123":        "ok_123",
		"a.b/c:d":       "a_b_c_d",
		"UPPER":         "UPPER",
		"":              "",
		"weïrd-uñicode": "we_rd_u_icode", // each non-ASCII rune becomes one '_'
	}
	for in, want := range tests {
		if got := sanitizeServerName(in); got != want {
			t.Errorf("sanitizeServerName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseSchema(t *testing.T) {
	t.Run("nil schema", func(t *testing.T) {
		params, required := parseSchema(nil)
		if params == nil || len(params) != 0 {
			t.Errorf("params = %v, want empty non-nil", params)
		}
		if required == nil || len(required) != 0 {
			t.Errorf("required = %v, want empty non-nil", required)
		}
	})

	t.Run("object with no properties", func(t *testing.T) {
		params, required := parseSchema(map[string]any{"type": "object"})
		if params == nil || len(params) != 0 {
			t.Errorf("params = %v, want empty non-nil", params)
		}
		if required == nil || len(required) != 0 {
			t.Errorf("required = %v, want empty non-nil", required)
		}
	})

	t.Run("with properties and required", func(t *testing.T) {
		schema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"a": map[string]any{"type": "string"},
				"b": map[string]any{"type": "integer"},
			},
			"required": []any{"a"},
		}
		params, required := parseSchema(schema)
		if _, ok := params["a"]; !ok {
			t.Errorf("params missing 'a': %v", params)
		}
		if _, ok := params["b"]; !ok {
			t.Errorf("params missing 'b': %v", params)
		}
		if len(required) != 1 || required[0] != "a" {
			t.Errorf("required = %v, want [a]", required)
		}
	})

	t.Run("unmarshalable schema falls back to empty", func(t *testing.T) {
		// A channel cannot be JSON-marshaled, so parseSchema returns the empty
		// (non-nil) defaults instead of panicking.
		params, required := parseSchema(make(chan int))
		if len(params) != 0 || len(required) != 0 {
			t.Errorf("want empty results on marshal failure, got params=%v required=%v", params, required)
		}
	})
}

func TestFlattenContent(t *testing.T) {
	t.Run("text blocks concatenate", func(t *testing.T) {
		got := flattenContent([]mcp.Content{
			&mcp.TextContent{Text: "foo"},
			&mcp.TextContent{Text: "bar"},
		})
		if got != "foobar" {
			t.Errorf("flattenContent = %q, want foobar", got)
		}
	})

	t.Run("non-text block is serialized as JSON", func(t *testing.T) {
		got := flattenContent([]mcp.Content{
			&mcp.TextContent{Text: "pre:"},
			&mcp.ImageContent{MIMEType: "image/png", Data: []byte("x")},
		})
		if !strings.HasPrefix(got, "pre:") {
			t.Errorf("result should keep the text prefix: %q", got)
		}
		if !strings.Contains(got, "image/png") {
			t.Errorf("non-text block should be serialized to JSON: %q", got)
		}
	})

	t.Run("empty", func(t *testing.T) {
		if got := flattenContent(nil); got != "" {
			t.Errorf("flattenContent(nil) = %q, want empty", got)
		}
	})
}

func TestRemoteToolProviderOptions(t *testing.T) {
	var tool fantasy.AgentTool = &remoteTool{}
	if tool.ProviderOptions() != nil {
		t.Error("initial ProviderOptions should be nil")
	}
	opts := fantasy.ProviderOptions{"prov": nil}
	tool.SetProviderOptions(opts)
	if got := tool.ProviderOptions(); len(got) != 1 {
		t.Errorf("ProviderOptions round-trip failed: %v", got)
	}
}
