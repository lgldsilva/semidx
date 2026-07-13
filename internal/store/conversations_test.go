package store

import (
	"context"
	"errors"
	"testing"
)

func TestConversationCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u, err := s.CreateUser(ctx, "dave", "hash", "member")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateUser(ctx, "erin", "hash", "member")
	if err != nil {
		t.Fatal(err)
	}

	// Create + get.
	c, err := s.CreateConversation(ctx, u.ID, "acme", "")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if c.ID == 0 || c.Title != "New chat" || c.Project != "acme" {
		t.Fatalf("conversation = %+v (title should default)", c)
	}
	got, err := s.GetConversation(ctx, u.ID, c.ID)
	if err != nil || got.ID != c.ID {
		t.Fatalf("GetConversation = %+v, err %v", got, err)
	}

	// Ownership isolation: another user cannot read it.
	if _, err := s.GetConversation(ctx, other.ID, c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-user GetConversation = %v, want ErrNotFound", err)
	}

	// Messages: add + list in order.
	if _, err := s.AddMessage(ctx, c.ID, "user", "how does auth work?", ""); err != nil {
		t.Fatalf("AddMessage user: %v", err)
	}
	if _, err := s.AddMessage(ctx, c.ID, "assistant", "it uses argon2id", `[{"file":"a.go"}]`); err != nil {
		t.Fatalf("AddMessage assistant: %v", err)
	}
	msgs, err := s.ListMessages(ctx, c.ID, 0)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("ListMessages = %d, err %v", len(msgs), err)
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" || msgs[1].SourcesJSON == "" {
		t.Errorf("messages out of order or missing sources: %+v", msgs)
	}

	// Rename (owner-scoped).
	if err := s.RenameConversation(ctx, u.ID, c.ID, "Auth deep-dive"); err != nil {
		t.Fatalf("RenameConversation: %v", err)
	}
	got, _ = s.GetConversation(ctx, u.ID, c.ID)
	if got.Title != "Auth deep-dive" {
		t.Errorf("title after rename = %q", got.Title)
	}
	if err := s.RenameConversation(ctx, other.ID, c.ID, "hijack"); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-user rename = %v, want ErrNotFound", err)
	}

	// List returns the user's conversation.
	list, err := s.ListConversations(ctx, u.ID, 0, 0)
	if err != nil || len(list) != 1 || list[0].ID != c.ID {
		t.Fatalf("ListConversations = %+v, err %v", list, err)
	}

	// Delete (owner-scoped) cascades messages.
	if err := s.DeleteConversation(ctx, other.ID, c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-user delete = %v, want ErrNotFound", err)
	}
	if err := s.DeleteConversation(ctx, u.ID, c.ID); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}
	if _, err := s.GetConversation(ctx, u.ID, c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("get after delete = %v, want ErrNotFound", err)
	}
	if msgs, _ := s.ListMessages(ctx, c.ID, 0); len(msgs) != 0 {
		t.Errorf("messages should cascade-delete, got %d", len(msgs))
	}
}
