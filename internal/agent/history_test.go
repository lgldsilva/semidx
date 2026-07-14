package agent

import (
	"testing"

	"charm.land/fantasy"

	"github.com/lgldsilva/semidx/internal/chat"
)

func TestMessagesFromChat(t *testing.T) {
	t.Parallel()
	got := MessagesFromChat([]chat.Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "system", Content: "s1"},
		{Role: "tool", Content: "dropped"}, // not representable → dropped
	})
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	wantRoles := []fantasy.MessageRole{
		fantasy.MessageRoleUser, fantasy.MessageRoleAssistant, fantasy.MessageRoleSystem,
	}
	wantText := []string{"q1", "a1", "s1"}
	for i, m := range got {
		if m.Role != wantRoles[i] {
			t.Errorf("msg[%d].Role = %v, want %v", i, m.Role, wantRoles[i])
		}
		if txt := messageText(m); txt != wantText[i] {
			t.Errorf("msg[%d] text = %q, want %q", i, txt, wantText[i])
		}
	}
}

func TestMessagesFromChatEmpty(t *testing.T) {
	t.Parallel()
	if got := MessagesFromChat(nil); len(got) != 0 {
		t.Fatalf("nil input should yield an empty slice, got %v", got)
	}
}
