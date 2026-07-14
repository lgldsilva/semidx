package localstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lgldsilva/semidx/internal/store"
)

// pause guarantees distinct millisecond timestamps between writes so recency
// ordering assertions are deterministic.
func pause() { time.Sleep(3 * time.Millisecond) }

func TestConversationCRUD(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	c1, err := s.CreateConversation(ctx, 0, "demo", "first chat")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if c1.ID == 0 || c1.UserID != 0 || c1.Project != "demo" || c1.Title != "first chat" {
		t.Fatalf("CreateConversation = %+v", c1)
	}
	if c1.CreatedAt.IsZero() || c1.UpdatedAt.IsZero() {
		t.Fatalf("expected timestamps, got %+v", c1)
	}

	// Empty title defaults, matching PgStore.
	c2, err := s.CreateConversation(ctx, 0, "", "")
	if err != nil {
		t.Fatalf("CreateConversation(empty title): %v", err)
	}
	if c2.Title != "New chat" {
		t.Fatalf("default title = %q, want %q", c2.Title, "New chat")
	}

	got, err := s.GetConversation(ctx, 0, c1.ID)
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if got.ID != c1.ID || got.Title != "first chat" {
		t.Fatalf("GetConversation = %+v", got)
	}

	if err := s.RenameConversation(ctx, 0, c1.ID, "renamed"); err != nil {
		t.Fatalf("RenameConversation: %v", err)
	}
	got, err = s.GetConversation(ctx, 0, c1.ID)
	if err != nil {
		t.Fatalf("GetConversation after rename: %v", err)
	}
	if got.Title != "renamed" {
		t.Fatalf("title after rename = %q", got.Title)
	}

	if err := s.DeleteConversation(ctx, 0, c1.ID); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}
	if _, err := s.GetConversation(ctx, 0, c1.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetConversation after delete = %v, want ErrNotFound", err)
	}
}

func TestConversationNotFoundErrors(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if _, err := s.GetConversation(ctx, 0, 12345); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetConversation(missing) = %v, want ErrNotFound", err)
	}
	if err := s.RenameConversation(ctx, 0, 12345, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("RenameConversation(missing) = %v, want ErrNotFound", err)
	}
	if err := s.DeleteConversation(ctx, 0, 12345); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("DeleteConversation(missing) = %v, want ErrNotFound", err)
	}
	// AddMessage to a dangling conversation must fail (FK enforced).
	if _, err := s.AddMessage(ctx, 12345, "user", "hi", ""); err == nil {
		t.Fatal("AddMessage(dangling conv) succeeded, want FK error")
	}
}

func TestConversationListOrdering(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	a, err := s.CreateConversation(ctx, 0, "p", "a")
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	pause()
	b, err := s.CreateConversation(ctx, 0, "p", "b")
	if err != nil {
		t.Fatalf("create b: %v", err)
	}

	convs, err := s.ListConversations(ctx, 0, 0, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 2 || convs[0].ID != b.ID || convs[1].ID != a.ID {
		t.Fatalf("initial order = %+v, want [b a]", convs)
	}

	// Adding a message bumps the conversation to the top.
	pause()
	if _, err := s.AddMessage(ctx, a.ID, "user", "hello", ""); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	convs, err = s.ListConversations(ctx, 0, 0, 0)
	if err != nil {
		t.Fatalf("ListConversations after message: %v", err)
	}
	if convs[0].ID != a.ID || convs[1].ID != b.ID {
		t.Fatalf("order after message = [%d %d], want [%d %d]", convs[0].ID, convs[1].ID, a.ID, b.ID)
	}

	// Limit and offset are honoured.
	page, err := s.ListConversations(ctx, 0, 1, 0)
	if err != nil || len(page) != 1 || page[0].ID != a.ID {
		t.Fatalf("ListConversations(limit=1) = %+v err=%v", page, err)
	}
	page, err = s.ListConversations(ctx, 0, 1, 1)
	if err != nil || len(page) != 1 || page[0].ID != b.ID {
		t.Fatalf("ListConversations(limit=1, offset=1) = %+v err=%v", page, err)
	}
}

func TestConversationUserIsolation(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	mine, err := s.CreateConversation(ctx, 0, "p", "mine")
	if err != nil {
		t.Fatalf("create mine: %v", err)
	}
	theirs, err := s.CreateConversation(ctx, 7, "p", "theirs")
	if err != nil {
		t.Fatalf("create theirs: %v", err)
	}

	convs, err := s.ListConversations(ctx, 0, 0, 0)
	if err != nil {
		t.Fatalf("ListConversations(user 0): %v", err)
	}
	if len(convs) != 1 || convs[0].ID != mine.ID {
		t.Fatalf("user 0 sees %+v, want only its own conversation", convs)
	}

	// Cross-user access is ErrNotFound on every owner-scoped operation.
	if _, err := s.GetConversation(ctx, 0, theirs.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetConversation(cross-user) = %v, want ErrNotFound", err)
	}
	if err := s.RenameConversation(ctx, 0, theirs.ID, "hijack"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("RenameConversation(cross-user) = %v, want ErrNotFound", err)
	}
	if err := s.DeleteConversation(ctx, 0, theirs.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("DeleteConversation(cross-user) = %v, want ErrNotFound", err)
	}
	if got, err := s.GetConversation(ctx, 7, theirs.ID); err != nil || got.Title != "theirs" {
		t.Fatalf("owner GetConversation = %+v err=%v", got, err)
	}
}

func TestConversationMessages(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	c, err := s.CreateConversation(ctx, 0, "p", "chat")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	m1, err := s.AddMessage(ctx, c.ID, "user", "what is this?", "")
	if err != nil {
		t.Fatalf("AddMessage user: %v", err)
	}
	if m1.ConvID != c.ID || m1.Role != "user" || m1.Content != "what is this?" || m1.SourcesJSON != "" {
		t.Fatalf("AddMessage = %+v", m1)
	}
	if m1.CreatedAt.IsZero() {
		t.Fatal("expected message timestamp")
	}
	m2, err := s.AddMessage(ctx, c.ID, "assistant", "an index", `[{"file":"a.go"}]`)
	if err != nil {
		t.Fatalf("AddMessage assistant: %v", err)
	}
	if m2.SourcesJSON != `[{"file":"a.go"}]` {
		t.Fatalf("SourcesJSON = %q", m2.SourcesJSON)
	}

	msgs, err := s.ListMessages(ctx, c.ID, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 2 || msgs[0].ID != m1.ID || msgs[1].ID != m2.ID {
		t.Fatalf("ListMessages = %+v, want chronological [m1 m2]", msgs)
	}

	// Limit caps from the start (chronological head), like PgStore's LIMIT.
	capped, err := s.ListMessages(ctx, c.ID, 1)
	if err != nil || len(capped) != 1 || capped[0].ID != m1.ID {
		t.Fatalf("ListMessages(limit=1) = %+v err=%v", capped, err)
	}

	// Deleting the conversation cascades to its messages.
	if err := s.DeleteConversation(ctx, 0, c.ID); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}
	msgs, err = s.ListMessages(ctx, c.ID, 0)
	if err != nil {
		t.Fatalf("ListMessages after delete: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("messages survived conversation delete: %+v", msgs)
	}
}

func TestParseSQLiteTime(t *testing.T) {
	// Millisecond precision (strftime %f) and legacy second precision both parse.
	ms := parseSQLiteTime("2026-07-14 10:20:30.123")
	if ms.IsZero() || ms.Nanosecond() != 123_000_000 {
		t.Fatalf("parseSQLiteTime(ms) = %v", ms)
	}
	sec := parseSQLiteTime("2026-07-14 10:20:30")
	if sec.IsZero() || sec.Second() != 30 {
		t.Fatalf("parseSQLiteTime(sec) = %v", sec)
	}
	if !parseSQLiteTime("garbage").IsZero() {
		t.Fatal("parseSQLiteTime(garbage) should be zero time")
	}
}
