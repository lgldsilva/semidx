package agent

import (
	"testing"

	"charm.land/fantasy"
)

// assistantWithToolCall builds the assistant+tool message pair a real turn
// produces, so trimming can be checked against orphaning risk.
func assistantWithToolCall(id string) []fantasy.Message {
	return []fantasy.Message{
		{
			Role:    fantasy.MessageRoleAssistant,
			Content: []fantasy.MessagePart{fantasy.ToolCallPart{ToolCallID: id, ToolName: "t", Input: "{}"}},
		},
		{
			Role:    fantasy.MessageRoleTool,
			Content: []fantasy.MessagePart{fantasy.ToolResultPart{ToolCallID: id, Output: fantasy.ToolResultOutputContentText{Text: "ok"}}},
		},
	}
}

func TestConversation_unbounded(t *testing.T) {
	c := NewConversation(0)
	for range 5 {
		c.AddUser("q")
		c.AddTurnMessages(assistantWithToolCall("c"))
	}
	// 5 turns * 3 messages each (user + assistant + tool).
	if c.Len() != 15 {
		t.Errorf("unbounded conversation should keep all messages, got %d", c.Len())
	}
}

func TestConversation_trimsWholeTurns(t *testing.T) {
	c := NewConversation(2)
	for i := range 4 {
		c.AddUser("q")
		_ = i
		c.AddTurnMessages(assistantWithToolCall("c"))
	}
	msgs := c.Messages()
	// Only the last 2 turns survive: 2 * (user + assistant + tool) = 6.
	if len(msgs) != 6 {
		t.Fatalf("want 6 messages (2 turns), got %d", len(msgs))
	}
	// The retained history must start at a user message — never mid-turn, or a
	// tool result would be orphaned from its assistant tool_call.
	if msgs[0].Role != fantasy.MessageRoleUser {
		t.Errorf("trimmed history must start on a user message, got role %q", msgs[0].Role)
	}
	// Every tool message must be preceded by its assistant tool_call.
	for i, m := range msgs {
		if m.Role == fantasy.MessageRoleTool {
			if i == 0 || msgs[i-1].Role != fantasy.MessageRoleAssistant {
				t.Errorf("tool message at %d not preceded by an assistant message", i)
			}
		}
	}
}

func TestConversation_clear(t *testing.T) {
	c := NewConversation(10)
	c.AddUser("q")
	c.AddTurnMessages(assistantWithToolCall("c"))
	c.Clear()
	if c.Len() != 0 {
		t.Errorf("Clear must empty the conversation, got %d", c.Len())
	}
}

func TestConversation_trimKeepsExactlyMaxTurns(t *testing.T) {
	c := NewConversation(3)
	// 3 plain user-only turns (no tool messages) keeps counting simple.
	for range 6 {
		c.AddUser("q")
	}
	if c.Len() != 3 {
		t.Errorf("want exactly 3 user turns retained, got %d", c.Len())
	}
}
