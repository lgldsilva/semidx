package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lgldsilva/semidx/internal/tenant"
)

// Conversation is one persisted chat thread owned by a user. Project is the
// bound project name, or "" for the global (all-projects) chat.
type Conversation struct {
	ID        int
	TenantID  int
	UserID    int
	Project   string
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ConversationMessage is one turn in a conversation. SourcesJSON carries the
// citation payload for assistant turns (empty for user turns).
type ConversationMessage struct {
	ID          int
	ConvID      int
	Role        string
	Content     string
	SourcesJSON string
	CreatedAt   time.Time
}

// ConversationStore persists multi-turn chat conversations. It is an optional
// extension implemented by PgStore; the admin type-asserts it, so stores that
// don't support it (SQLite, remote client) simply disable the feature.
type ConversationStore interface {
	CreateConversation(ctx context.Context, userID int, project, title string) (*Conversation, error)
	ListConversations(ctx context.Context, userID, limit, offset int) ([]Conversation, error)
	// GetConversation is scoped to the owning user; ErrNotFound otherwise.
	GetConversation(ctx context.Context, userID, id int) (*Conversation, error)
	RenameConversation(ctx context.Context, userID, id int, title string) error
	DeleteConversation(ctx context.Context, userID, id int) error
	AddMessage(ctx context.Context, convID int, role, content, sourcesJSON string) (*ConversationMessage, error)
	// ListMessages returns a conversation's messages in chronological order,
	// capped by limit (<=0 = all).
	ListMessages(ctx context.Context, convID, limit int) ([]ConversationMessage, error)
}

// CreateConversation inserts a new conversation for a user.
func (s *PgStore) CreateConversation(ctx context.Context, userID int, project, title string) (*Conversation, error) {
	if title == "" {
		title = "New chat"
	}
	var c Conversation
	err := s.pool.QueryRow(ctx, `
		INSERT INTO conversations (tenant_id, user_id, project, title) VALUES ($1, $2, $3, $4)
		RETURNING id, tenant_id, user_id, project, title, created_at, updated_at
	`, tenant.ID(ctx), userID, project, title).Scan(&c.ID, &c.TenantID, &c.UserID, &c.Project, &c.Title, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ListConversations returns a user's conversations, most-recently-updated first.
func (s *PgStore) ListConversations(ctx context.Context, userID, limit, offset int) ([]Conversation, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, user_id, project, title, created_at, updated_at
		FROM conversations WHERE tenant_id = $1 AND user_id = $2
		ORDER BY updated_at DESC LIMIT $3 OFFSET $4
	`, tenant.ID(ctx), userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.TenantID, &c.UserID, &c.Project, &c.Title, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetConversation returns a conversation owned by userID, or ErrNotFound.
func (s *PgStore) GetConversation(ctx context.Context, userID, id int) (*Conversation, error) {
	var c Conversation
	err := s.pool.QueryRow(ctx, `
		SELECT id, tenant_id, user_id, project, title, created_at, updated_at
		FROM conversations WHERE tenant_id = $1 AND id = $2 AND user_id = $3
	`, tenant.ID(ctx), id, userID).Scan(&c.ID, &c.TenantID, &c.UserID, &c.Project, &c.Title, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// RenameConversation updates a conversation's title (owner-scoped).
func (s *PgStore) RenameConversation(ctx context.Context, userID, id int, title string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE conversations SET title = $1, updated_at = NOW() WHERE tenant_id = $2 AND id = $3 AND user_id = $4
	`, title, tenant.ID(ctx), id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteConversation removes a conversation (and its messages via cascade).
func (s *PgStore) DeleteConversation(ctx context.Context, userID, id int) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM conversations WHERE tenant_id = $1 AND id = $2 AND user_id = $3`, tenant.ID(ctx), id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AddMessage appends a message to a conversation and bumps its updated_at.
func (s *PgStore) AddMessage(ctx context.Context, convID int, role, content, sourcesJSON string) (*ConversationMessage, error) {
	var m ConversationMessage
	err := s.pool.QueryRow(ctx, `
		INSERT INTO conversation_messages (conversation_id, role, content, sources_json)
		SELECT id, $2, $3, $4 FROM conversations WHERE id = $1 AND tenant_id = $5
		RETURNING id, conversation_id, role, content, sources_json, created_at
	`, convID, role, content, sourcesJSON, tenant.ID(ctx)).Scan(&m.ID, &m.ConvID, &m.Role, &m.Content, &m.SourcesJSON, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	// Best-effort bump so the conversation sorts to the top of the list.
	_, _ = s.pool.Exec(ctx, `UPDATE conversations SET updated_at = NOW() WHERE tenant_id = $1 AND id = $2`, tenant.ID(ctx), convID)
	return &m, nil
}

// ListMessages returns a conversation's messages in chronological order.
func (s *PgStore) ListMessages(ctx context.Context, convID, limit int) ([]ConversationMessage, error) {
	q := `SELECT m.id, m.conversation_id, m.role, m.content, m.sources_json, m.created_at
		FROM conversation_messages m JOIN conversations c ON c.id = m.conversation_id
		WHERE m.conversation_id = $1 AND c.tenant_id = $2 ORDER BY m.id`
	args := []any{convID, tenant.ID(ctx)}
	if limit > 0 {
		q += ` LIMIT $3`
		args = append(args, limit)
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConversationMessage
	for rows.Next() {
		var m ConversationMessage
		if err := rows.Scan(&m.ID, &m.ConvID, &m.Role, &m.Content, &m.SourcesJSON, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
