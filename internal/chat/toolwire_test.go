package chat

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestToolCallWireFormat is the regression test for the tool-calling wire fix.
// The assistant message that requests tools must serialize a tool_calls array,
// and the tool message answering it must carry tool_call_id — without these a
// strict OpenAI-compatible provider (e.g. OpenRouter) rejects the request.
func TestToolCallWireFormat(t *testing.T) {
	req := Request{
		Messages: []Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "search", Args: `{"q":"x"}`}}},
			{Role: "tool", ToolCallID: "call_1", Content: `{"ok":true}`},
		},
	}
	data, err := json.Marshal(buildOpenAIChatRequest(req, false))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)
	for _, want := range []string{
		`"tool_calls"`,
		`"tool_call_id":"call_1"`,
		`"id":"call_1"`,
		`"type":"function"`,
		`"name":"search"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("wire request missing %s\n got: %s", want, s)
		}
	}
}

// TestPlainMessagesOmitToolFields verifies non-tool messages stay byte-identical
// to the pre-fix wire (omitempty), so ordinary chat is unaffected.
func TestPlainMessagesOmitToolFields(t *testing.T) {
	req := Request{Messages: []Message{
		{Role: "system", Content: "be brief"},
		{Role: "user", Content: "hi"},
	}}
	data, err := json.Marshal(buildOpenAIChatRequest(req, false))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)
	if strings.Contains(s, "tool_calls") || strings.Contains(s, "tool_call_id") {
		t.Errorf("plain messages must omit tool fields: %s", s)
	}
}
