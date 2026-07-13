package webadmin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lgldsilva/semidx/internal/store"
)

// --- in-memory ConversationStore on the test fakeStore ---

func (f *fakeStore) CreateConversation(_ context.Context, userID int, project, title string) (*store.Conversation, error) {
	if title == "" {
		title = "New chat"
	}
	f.nextConv++
	c := &store.Conversation{ID: f.nextConv, UserID: userID, Project: project, Title: title, CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)}
	f.convs[c.ID] = c
	return c, nil
}

func (f *fakeStore) ListConversations(_ context.Context, userID, _, _ int) ([]store.Conversation, error) {
	var out []store.Conversation
	for _, c := range f.convs {
		if c.UserID == userID {
			out = append(out, *c)
		}
	}
	return out, nil
}

func (f *fakeStore) GetConversation(_ context.Context, userID, id int) (*store.Conversation, error) {
	c, ok := f.convs[id]
	if !ok || c.UserID != userID {
		return nil, store.ErrNotFound
	}
	return c, nil
}

func (f *fakeStore) RenameConversation(_ context.Context, userID, id int, title string) error {
	c, ok := f.convs[id]
	if !ok || c.UserID != userID {
		return store.ErrNotFound
	}
	c.Title = title
	return nil
}

func (f *fakeStore) DeleteConversation(_ context.Context, userID, id int) error {
	c, ok := f.convs[id]
	if !ok || c.UserID != userID {
		return store.ErrNotFound
	}
	delete(f.convs, id)
	delete(f.convMsgs, id)
	return nil
}

func (f *fakeStore) AddMessage(_ context.Context, convID int, role, content, sourcesJSON string) (*store.ConversationMessage, error) {
	f.nextMsg++
	m := store.ConversationMessage{ID: f.nextMsg, ConvID: convID, Role: role, Content: content, SourcesJSON: sourcesJSON, CreatedAt: time.Unix(1, 0)}
	f.convMsgs[convID] = append(f.convMsgs[convID], m)
	return &m, nil
}

func (f *fakeStore) ListMessages(_ context.Context, convID, _ int) ([]store.ConversationMessage, error) {
	return f.convMsgs[convID], nil
}

// --- tests ---

func TestConversationsAPI(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	// System advertises the conversations capability.
	if _, body := getBody(t, c, srv.URL+"/admin/api/system"); !strings.Contains(body, "conversations") {
		t.Errorf("system caps should include conversations: %s", body)
	}

	// Create.
	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/conversations", csrf, map[string]any{
		"project": "acme", "title": "Auth",
	})
	if code != 200 || !strings.Contains(body, `"title":"Auth"`) {
		t.Fatalf("create = %d body=%s", code, body)
	}
	var created struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.ID == 0 {
		t.Fatalf("parse id: %v body=%s", err, body)
	}
	base := srv.URL + "/admin/api/conversations/" + strconv.Itoa(created.ID)

	// Append a user + assistant message (with sources).
	if code, _ = postAdminJSON(t, c, base+"/messages", csrf, map[string]any{
		"role": "user", "content": "how does auth work?",
	}); code != 200 {
		t.Fatalf("add user msg = %d", code)
	}
	if code, _ = postAdminJSON(t, c, base+"/messages", csrf, map[string]any{
		"role": "assistant", "content": "argon2id", "sources": []map[string]any{{"file": "a.go", "start_line": 1}},
	}); code != 200 {
		t.Fatalf("add assistant msg = %d", code)
	}

	// Get returns the messages with sources surfaced as JSON.
	code, body = getBody(t, c, base)
	if code != 200 || !strings.Contains(body, `"content":"argon2id"`) || !strings.Contains(body, `"file":"a.go"`) {
		t.Fatalf("get = %d body=%s", code, body)
	}

	// List returns the conversation.
	code, body = getBody(t, c, srv.URL+"/admin/api/conversations")
	if code != 200 || !strings.Contains(body, `"title":"Auth"`) {
		t.Fatalf("list = %d body=%s", code, body)
	}

	// Rename.
	if st := doJSON(t, c, http.MethodPatch, base, csrf, `{"title":"Auth v2"}`); st != 200 {
		t.Fatalf("rename = %d", st)
	}

	// Delete, then it's gone.
	if st := doJSON(t, c, http.MethodDelete, base, csrf, ""); st != 200 {
		t.Fatalf("delete = %d", st)
	}
	if code, _ := getBody(t, c, base); code != http.StatusNotFound {
		t.Errorf("get after delete = %d, want 404", code)
	}
}

// doJSON issues a request with a CSRF token and returns the status code.
func doJSON(t *testing.T, c *http.Client, method, url, csrf, body string) int {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}
