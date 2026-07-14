package agent

import (
	"charm.land/fantasy"

	"github.com/lgldsilva/semidx/internal/chat"
)

// MessagesFromChat converts text-only chat.Message turns into fantasy messages
// for a Runner. Roles other than user/system/assistant are dropped (tool turns
// are not representable in chat.Message; full tool_call history is Phase 5).
func MessagesFromChat(msgs []chat.Message) []fantasy.Message {
	out := make([]fantasy.Message, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "user":
			out = append(out, fantasy.NewUserMessage(m.Content))
		case "system":
			out = append(out, fantasy.NewSystemMessage(m.Content))
		case "assistant":
			out = append(out, fantasy.Message{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: m.Content}},
			})
		}
	}
	return out
}
