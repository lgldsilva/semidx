package agent

import "charm.land/fantasy"

// Conversation accumulates fantasy messages across turns so the agent keeps
// real multi-turn tool memory: the assistant tool_calls and their tool results
// stay in the history, letting the model reference earlier tool output instead
// of re-deriving it. This is what the text-only chat.History could not carry.
//
// Growth is bounded by a turn count (a turn = one user message and everything
// the assistant/tools produced in reply). Trimming drops whole turns from the
// front so a tool-result message is never orphaned from the assistant
// tool_call that produced it — strict providers reject that pairing.
type Conversation struct {
	msgs     []fantasy.Message
	maxTurns int // <= 0 means unbounded
}

// NewConversation returns an empty conversation bounded to maxTurns user turns
// (0 = unbounded).
func NewConversation(maxTurns int) *Conversation {
	return &Conversation{maxTurns: maxTurns}
}

// Messages returns the accumulated messages in order. The slice is owned by the
// Conversation; callers must not mutate it.
func (c *Conversation) Messages() []fantasy.Message {
	return c.msgs
}

// AddUser appends a user message, opening a new turn.
func (c *Conversation) AddUser(text string) {
	c.msgs = append(c.msgs, fantasy.NewUserMessage(text))
	c.trim()
}

// AddTurnMessages appends the assistant/tool messages produced in reply to the
// most recent user message (typically Answer.Messages).
func (c *Conversation) AddTurnMessages(msgs []fantasy.Message) {
	c.msgs = append(c.msgs, msgs...)
	c.trim()
}

// Clear resets the conversation.
func (c *Conversation) Clear() {
	c.msgs = nil
}

// Len reports the number of messages currently retained.
func (c *Conversation) Len() int { return len(c.msgs) }

// trim drops the oldest turns until at most maxTurns user messages remain,
// cutting only at user-message boundaries so assistant+tool pairs stay intact.
func (c *Conversation) trim() {
	if c.maxTurns <= 0 {
		return
	}
	// Find user-message indices.
	var userIdx []int
	for i, m := range c.msgs {
		if m.Role == fantasy.MessageRoleUser {
			userIdx = append(userIdx, i)
		}
	}
	if len(userIdx) <= c.maxTurns {
		return
	}
	// Keep from the start of the (len-maxTurns)-th user message onward.
	cut := userIdx[len(userIdx)-c.maxTurns]
	c.msgs = c.msgs[cut:]
}
