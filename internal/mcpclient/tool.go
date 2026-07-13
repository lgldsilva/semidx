package mcpclient

import (
	"context"
	"encoding/json"
	"strings"

	"charm.land/fantasy"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// remoteTool adapts a single remote MCP tool to the fantasy.AgentTool interface.
// The name exposed to the model is namespaced (see [Session.Tools]), while
// rawName is the original tool name used when calling the server.
type remoteTool struct {
	session         *Session
	rawName         string
	name            string
	description     string
	parameters      map[string]any
	required        []string
	providerOptions fantasy.ProviderOptions
}

// Ensure remoteTool satisfies the interface at compile time.
var _ fantasy.AgentTool = (*remoteTool)(nil)

// Info returns the tool metadata, with the dynamic schema mapped from the MCP
// tool's InputSchema.
func (t *remoteTool) Info() fantasy.ToolInfo {
	return fantasy.ToolInfo{
		Name:        t.name,
		Description: t.description,
		Parameters:  t.parameters,
		Required:    t.required,
		Parallel:    false,
	}
}

// Run forwards the call to the remote server and flattens the result into a
// fantasy.ToolResponse.
//
// params.Input is the model-supplied argument JSON; it is passed through
// verbatim (an empty input becomes no arguments). Text content is concatenated;
// a tool-reported error (result.IsError) becomes an error response. A transport
// failure is reported as an error response rather than a returned error, so a
// single failing tool call does not abort the agent's loop.
func (t *remoteTool) Run(ctx context.Context, params fantasy.ToolCall) (fantasy.ToolResponse, error) {
	var args any
	if strings.TrimSpace(params.Input) != "" {
		args = json.RawMessage(params.Input)
	}
	res, err := t.session.cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      t.rawName,
		Arguments: args,
	})
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	text := flattenContent(res.Content)
	if res.IsError {
		return fantasy.NewTextErrorResponse(text), nil
	}
	return fantasy.NewTextResponse(text), nil
}

// ProviderOptions returns the stored provider options.
func (t *remoteTool) ProviderOptions() fantasy.ProviderOptions { return t.providerOptions }

// SetProviderOptions stores the provider options.
func (t *remoteTool) SetProviderOptions(opts fantasy.ProviderOptions) { t.providerOptions = opts }

// flattenContent concatenates the text of every content block. Text blocks
// contribute their text; any other block type is serialized to its JSON form so
// no information is silently dropped.
func flattenContent(contents []mcp.Content) string {
	var b strings.Builder
	for _, c := range contents {
		switch v := c.(type) {
		case *mcp.TextContent:
			b.WriteString(v.Text)
		default:
			if raw, err := c.MarshalJSON(); err == nil {
				b.Write(raw)
			}
		}
	}
	return b.String()
}

// parseSchema maps an MCP tool's InputSchema (a JSON Schema object, delivered to
// the client as a map[string]any) into fantasy's ToolInfo shape: the schema's
// "properties" object becomes Parameters, and its "required" array becomes
// Required. Both are always non-nil, even when the schema is absent or has no
// such fields.
func parseSchema(inputSchema any) (map[string]any, []string) {
	params := map[string]any{}
	required := []string{}
	if inputSchema == nil {
		return params, required
	}
	raw, err := json.Marshal(inputSchema)
	if err != nil {
		return params, required
	}
	var parsed struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return params, required
	}
	if parsed.Properties != nil {
		params = parsed.Properties
	}
	if parsed.Required != nil {
		required = parsed.Required
	}
	return params, required
}
