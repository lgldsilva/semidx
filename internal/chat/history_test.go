package chat

import (
	"testing"
)

func TestHistoryAddMessages(t *testing.T) {
	t.Parallel()

	h := NewHistory(0) // unlimited
	h.AddUser("hello")
	h.AddAssistant("hi there")
	h.AddUser("how are you?")

	msgs := h.GetMessages()
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "hello" {
		t.Errorf("msg[0] = %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "hi there" {
		t.Errorf("msg[1] = %+v", msgs[1])
	}
	if msgs[2].Role != "user" || msgs[2].Content != "how are you?" {
		t.Errorf("msg[2] = %+v", msgs[2])
	}
}

func TestHistoryAddSystem(t *testing.T) {
	t.Parallel()

	h := NewHistory(0)
	h.AddSystem("You are a bot.")
	h.AddUser("hello")

	msgs := h.GetMessages()
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Errorf("msg[0].Role = %q, want system", msgs[0].Role)
	}
}

func TestHistoryMaxTurnsTrimming(t *testing.T) {
	t.Parallel()

	h := NewHistory(2) // keep 2 turns
	h.AddUser("q1")
	h.AddAssistant("a1")
	h.AddUser("q2")
	h.AddAssistant("a2")
	h.AddUser("q3")
	h.AddAssistant("a3")

	msgs := h.GetMessages()
	// Should keep only the last 2 turns = 4 messages.
	if len(msgs) != 4 {
		t.Fatalf("got %d messages, want 4", len(msgs))
	}
	if msgs[0].Content != "q2" {
		t.Errorf("first kept msg = %q, want q2", msgs[0].Content)
	}
	if msgs[3].Content != "a3" {
		t.Errorf("last msg = %q, want a3", msgs[3].Content)
	}
}

func TestHistoryMaxTurnsWithSystem(t *testing.T) {
	t.Parallel()

	h := NewHistory(1) // keep 1 turn
	h.AddSystem("You are a bot.")
	h.AddUser("q1")
	h.AddAssistant("a1")
	h.AddUser("q2")
	h.AddAssistant("a2")

	msgs := h.GetMessages()
	// Should keep system + last 1 turn (2 messages) = 3 messages total.
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3 (system + 1 turn)", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Errorf("first msg should be system, got %q", msgs[0].Role)
	}
	if msgs[1].Content != "q2" {
		t.Errorf("second msg = %q, want q2", msgs[1].Content)
	}
	if msgs[2].Content != "a2" {
		t.Errorf("third msg = %q, want a2", msgs[2].Content)
	}
}

func TestHistoryClear(t *testing.T) {
	t.Parallel()

	h := NewHistory(0)
	h.AddUser("hello")
	h.AddAssistant("world")
	h.Clear()

	msgs := h.GetMessages()
	if len(msgs) != 0 {
		t.Errorf("after clear, got %d messages, want 0", len(msgs))
	}
}

func TestHistoryGetMessagesIsCopy(t *testing.T) {
	t.Parallel()

	h := NewHistory(0)
	h.AddUser("hello")

	msgs := h.GetMessages()
	msgs[0].Content = "mutated"

	// Original should be unchanged.
	original := h.GetMessages()
	if original[0].Content != "hello" {
		t.Errorf("original was mutated: got %q, want hello", original[0].Content)
	}
}

func TestHistoryUnlimitedNoTrimming(t *testing.T) {
	t.Parallel()

	h := NewHistory(0) // unlimited
	for i := 0; i < 100; i++ {
		h.AddUser("q")
		h.AddAssistant("a")
	}

	msgs := h.GetMessages()
	if len(msgs) != 200 {
		t.Errorf("got %d messages, want 200 (unlimited)", len(msgs))
	}
}
