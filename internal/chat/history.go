package chat

// History manages a conversation's message history.
type History struct {
	Messages []Message
	MaxTurns int // maximum number of Q&A turns to keep (0 = unlimited)
}

// NewHistory creates a conversation history. maxTurns is the maximum number of
// Q&A turns (each turn is a user+assistant pair) to retain; 0 means unlimited.
func NewHistory(maxTurns int) *History {
	return &History{
		Messages: make([]Message, 0),
		MaxTurns: maxTurns,
	}
}

// AddUser appends a user message.
func (h *History) AddUser(content string) {
	h.Messages = append(h.Messages, Message{Role: "user", Content: content})
	h.trim()
}

// AddAssistant appends an assistant message.
func (h *History) AddAssistant(content string) {
	h.Messages = append(h.Messages, Message{Role: "assistant", Content: content})
	h.trim()
}

// AddSystem appends a system message.
func (h *History) AddSystem(content string) {
	h.Messages = append(h.Messages, Message{Role: "system", Content: content})
	h.trim()
}

// GetMessages returns a copy of the message history.
func (h *History) GetMessages() []Message {
	out := make([]Message, len(h.Messages))
	copy(out, h.Messages)
	return out
}

// Clear removes all messages from the history.
func (h *History) Clear() {
	h.Messages = h.Messages[:0]
}

// trim removes oldest messages when the history exceeds MaxTurns turns. Each
// turn is a user+assistant pair (2 messages). System messages at the start are
// preserved. A trailing unpaired user message (the current question) is counted
// as part of the newest turn but doesn't force an extra turn to be evicted.
func (h *History) trim() {
	if h.MaxTurns <= 0 || len(h.Messages) == 0 {
		return
	}

	// Count system messages at the start to preserve them.
	var systemCount int
	for _, m := range h.Messages {
		if m.Role == "system" {
			systemCount++
		} else {
			break
		}
	}

	// Count user messages (turns) among non-system messages.
	var userCount int
	for i := systemCount; i < len(h.Messages); i++ {
		if h.Messages[i].Role == "user" {
			userCount++
		}
	}

	if userCount <= h.MaxTurns {
		return
	}

	// We need to remove (userCount - MaxTurns) complete turns from the front.
	toRemove := userCount - h.MaxTurns

	// Find the cut point: skip toRemove full user+assistant pairs.
	cut := systemCount
	for cut < len(h.Messages) && toRemove > 0 {
		if h.Messages[cut].Role == "user" {
			toRemove--
			cut++ // skip the user
			// Skip the corresponding assistant if present.
			if cut < len(h.Messages) && h.Messages[cut].Role == "assistant" {
				cut++
			}
		} else {
			cut++ // shouldn't normally reach here
		}
	}

	// Keep system messages + everything from the cut point onward.
	kept := make([]Message, 0, systemCount+len(h.Messages)-cut)
	kept = append(kept, h.Messages[:systemCount]...)
	kept = append(kept, h.Messages[cut:]...)
	h.Messages = kept
}
