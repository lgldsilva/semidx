package localstore

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lgldsilva/semidx/internal/store"
)

// compile-time assertion that SQLiteStore also satisfies the optional
// conversation extension consumed by the chat UIs.
var _ store.ConversationStore = (*SQLiteStore)(nil)

// conversationColumns is the canonical projection shared by the conversation
// getters so scanConversation can read any of them.
const conversationColumns = `id, user_id, project, title, created_at, updated_at`

// sqliteNowMillis is the SQL expression used for conversation timestamps: UTC
// with millisecond precision, so recency ordering survives bursts of writes
// that datetime('now')'s one-second resolution would collapse into ties.
const sqliteNowMillis = `strftime('%Y-%m-%d %H:%M:%f','now')`

// sqliteTimeLayout parses the strftime output above. time.Parse accepts an
// optional fractional-second field even when the layout omits it, so the same
// layout also reads legacy second-resolution values.
const sqliteTimeLayout = "2006-01-02 15:04:05"

// parseSQLiteTime converts a stored UTC timestamp string to time.Time. An
// unparseable value (hand-edited DB) degrades to the zero time rather than
// failing the whole query — timestamps are display metadata here.
func parseSQLiteTime(s string) time.Time {
	t, err := time.Parse(sqliteTimeLayout, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func scanConversation(row interface{ Scan(...any) error }) (*store.Conversation, error) {
	var (
		c                store.Conversation
		created, updated string
	)
	if err := row.Scan(&c.ID, &c.UserID, &c.Project, &c.Title, &created, &updated); err != nil {
		return nil, err
	}
	c.CreatedAt = parseSQLiteTime(created)
	c.UpdatedAt = parseSQLiteTime(updated)
	return &c, nil
}

// CreateConversation inserts a new conversation owned by userID. The local
// binary is single-user and passes userID=0, but the column is honoured so the
// semantics match PgStore.
func (s *SQLiteStore) CreateConversation(ctx context.Context, userID int, project, title string) (*store.Conversation, error) {
	if title == "" {
		title = "New chat"
	}
	return scanConversation(s.db.QueryRowContext(ctx, `
		INSERT INTO conversations (user_id, project, title) VALUES (?, ?, ?)
		RETURNING `+conversationColumns, userID, project, title))
}

// ListConversations returns a user's conversations, most-recently-updated
// first (id breaks same-millisecond ties deterministically).
func (s *SQLiteStore) ListConversations(ctx context.Context, userID, limit, offset int) ([]store.Conversation, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+conversationColumns+` FROM conversations WHERE user_id = ?
		ORDER BY updated_at DESC, id DESC LIMIT ? OFFSET ?`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []store.Conversation
	for rows.Next() {
		c, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// GetConversation returns a conversation owned by userID, or store.ErrNotFound.
func (s *SQLiteStore) GetConversation(ctx context.Context, userID, id int) (*store.Conversation, error) {
	c, err := scanConversation(s.db.QueryRowContext(ctx, `
		SELECT `+conversationColumns+` FROM conversations WHERE id = ? AND user_id = ?`, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return c, err
}

// RenameConversation updates a conversation's title (owner-scoped).
func (s *SQLiteStore) RenameConversation(ctx context.Context, userID, id int, title string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE conversations SET title = ?, updated_at = `+sqliteNowMillis+`
		WHERE id = ? AND user_id = ?`, title, id, userID)
	return oneRowOrNotFound(res, err)
}

// DeleteConversation removes a conversation; its messages cascade.
func (s *SQLiteStore) DeleteConversation(ctx context.Context, userID, id int) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM conversations WHERE id = ? AND user_id = ?`, id, userID)
	return oneRowOrNotFound(res, err)
}

// oneRowOrNotFound maps a zero-row write to store.ErrNotFound.
func oneRowOrNotFound(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// AddMessage appends a message to a conversation and bumps its updated_at so
// the conversation sorts to the top of the list. A dangling convID fails the
// foreign-key check (foreign_keys=1 is set per connection).
func (s *SQLiteStore) AddMessage(ctx context.Context, convID int, role, content, sourcesJSON string) (*store.ConversationMessage, error) {
	var (
		m       store.ConversationMessage
		created string
	)
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO conversation_messages (conversation_id, role, content, sources_json)
		VALUES (?, ?, ?, ?)
		RETURNING id, conversation_id, role, content, sources_json, created_at`,
		convID, role, content, sourcesJSON).
		Scan(&m.ID, &m.ConvID, &m.Role, &m.Content, &m.SourcesJSON, &created)
	if err != nil {
		return nil, err
	}
	m.CreatedAt = parseSQLiteTime(created)
	// Best-effort bump, mirroring PgStore.
	_, _ = s.db.ExecContext(ctx,
		`UPDATE conversations SET updated_at = `+sqliteNowMillis+` WHERE id = ?`, convID)
	return &m, nil
}

// ListMessages returns a conversation's messages in chronological order,
// capped by limit (<=0 = all).
func (s *SQLiteStore) ListMessages(ctx context.Context, convID, limit int) ([]store.ConversationMessage, error) {
	if limit <= 0 {
		limit = -1 // SQLite treats LIMIT -1 as "no limit"
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, conversation_id, role, content, sources_json, created_at
		FROM conversation_messages WHERE conversation_id = ?
		ORDER BY id LIMIT ?`, convID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []store.ConversationMessage
	for rows.Next() {
		var (
			m       store.ConversationMessage
			created string
		)
		if err := rows.Scan(&m.ID, &m.ConvID, &m.Role, &m.Content, &m.SourcesJSON, &created); err != nil {
			return nil, err
		}
		m.CreatedAt = parseSQLiteTime(created)
		out = append(out, m)
	}
	return out, rows.Err()
}
