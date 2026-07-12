package agent

import (
	"context"
	"testing"

	"github.com/lgldsilva/semidx/internal/chat"
)

var ctx = context.Background()

// fakeTool returns a fixed result.
type fakeTool struct{}

func (fakeTool) Def() chat.ToolDef {
	return chat.ToolDef{Name: "test_tool", Description: "test", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}
}
func (fakeTool) Run(_ context.Context, args string) (string, error) {
	return `{"result":"ok","args":` + args + `}`, nil
}

func TestAgentAskNoTools(t *testing.T) {
	client := &fakeChatClient{response: "Hello world"}
	agent := NewAgent(client, nil, nil)
	ans, err := agent.Ask(ctx, "hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ans.Content != "Hello world" {
		t.Errorf("got %q", ans.Content)
	}
}

func TestAgentAskWithToolCall(t *testing.T) {
	client := &fakeChatClient{
		response:  "",
		toolCalls: []chat.ToolCall{{ID: "call1", Name: "test_tool", Args: `{"x":1}`}},
	}
	agent := NewAgent(client, []Tool{fakeTool{}}, nil)
	ans, err := agent.Ask(ctx, "use tool", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ans.Trace) != 1 {
		t.Fatal("expected 1 tool call")
	}
	if ans.Trace[0].Result != `{"result":"ok","args":{"x":1}}` {
		t.Errorf("unexpected result: %q", ans.Trace[0].Result)
	}
}

// fakeChatClient implements chat.Client, returning tool calls only once.
type fakeChatClient struct {
	response   string
	toolCalls  []chat.ToolCall
	returnedTC bool // tracks whether we already returned the tool calls
}

func (f *fakeChatClient) SendMessage(_ context.Context, req chat.Request) (*chat.Response, error) {
	if len(f.toolCalls) > 0 && !f.returnedTC {
		f.returnedTC = true
		return &chat.Response{ToolCalls: f.toolCalls}, nil
	}
	return &chat.Response{Content: f.response}, nil
}
