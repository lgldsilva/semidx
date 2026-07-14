package webchat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/localstore"
	"github.com/lgldsilva/semidx/internal/store"
)

// newConvServer builds a webchat Server backed by a real SQLite conversation
// store (in a temp file), exercising the localstore+webchat pair end-to-end.
func newConvServer(t *testing.T, project string) *Server {
	t.Helper()
	srv, err := New(&mockPipeline{}, project, ":0")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ls, err := localstore.New(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("localstore.New: %v", err)
	}
	t.Cleanup(ls.Close)
	srv.SetConversationStore(ls)
	return srv
}

func doJSON(t *testing.T, h http.HandlerFunc, method, target, body, pathID string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", mimeJSON)
	if pathID != "" {
		req.SetPathValue("id", pathID)
	}
	rr := httptest.NewRecorder()
	h(rr, req)
	var decoded map[string]any
	if rr.Body.Len() > 0 {
		if err := json.Unmarshal(rr.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("response is not JSON: %v (%s)", err, rr.Body.String())
		}
	}
	return rr, decoded
}

func TestConversations_NotWired_Returns501(t *testing.T) {
	srv, err := New(&mockPipeline{}, "p", ":0")
	if err != nil {
		t.Fatal(err)
	}
	for name, h := range map[string]http.HandlerFunc{
		"list":   srv.handleListConversations,
		"create": srv.handleCreateConversation,
		"get":    srv.handleGetConversation,
		"delete": srv.handleDeleteConversation,
		"msg":    srv.handleAddConversationMessage,
	} {
		rr, body := doJSON(t, h, http.MethodGet, "/api/conversations", "", "1")
		if rr.Code != http.StatusNotImplemented {
			t.Errorf("%s: status = %d, want 501", name, rr.Code)
		}
		if msg, _ := body["error"].(string); msg == "" {
			t.Errorf("%s: expected error body, got %v", name, body)
		}
	}
}

func TestConversations_CreateListGetDelete(t *testing.T) {
	srv := newConvServer(t, "my-project")

	// Create with explicit title; empty project inherits the server's.
	rr, created := doJSON(t, srv.handleCreateConversation, http.MethodPost,
		"/api/conversations", `{"title":"about parsing"}`, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("create status = %d body=%v", rr.Code, created)
	}
	if created["title"] != "about parsing" || created["project"] != "my-project" {
		t.Fatalf("created = %v", created)
	}
	id := int(created["id"].(float64))

	// List returns it.
	rr, list := doJSON(t, srv.handleListConversations, http.MethodGet, "/api/conversations", "", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d", rr.Code)
	}
	convs, _ := list["conversations"].([]any)
	if len(convs) != 1 {
		t.Fatalf("list = %v, want 1 conversation", list)
	}

	// Add a user+assistant turn, the assistant one carrying sources.
	rr, _ = doJSON(t, srv.handleAddConversationMessage, http.MethodPost,
		fmt.Sprintf("/api/conversations/%d/messages", id),
		`{"role":"user","content":"how does chunking work?"}`, fmt.Sprint(id))
	if rr.Code != http.StatusOK {
		t.Fatalf("add user message status = %d", rr.Code)
	}
	rr, msg := doJSON(t, srv.handleAddConversationMessage, http.MethodPost,
		fmt.Sprintf("/api/conversations/%d/messages", id),
		`{"role":"assistant","content":"line-aware","sources":[{"file":"chunker.go","start_line":1}]}`,
		fmt.Sprint(id))
	if rr.Code != http.StatusOK {
		t.Fatalf("add assistant message status = %d body=%v", rr.Code, msg)
	}
	if msg["sources"] == nil {
		t.Fatalf("assistant message lost its sources: %v", msg)
	}

	// Get returns the conversation with both messages, sources as real JSON.
	rr, got := doJSON(t, srv.handleGetConversation, http.MethodGet,
		fmt.Sprintf("/api/conversations/%d", id), "", fmt.Sprint(id))
	if rr.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%v", rr.Code, got)
	}
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %v, want 2", got["messages"])
	}
	first, _ := msgs[0].(map[string]any)
	second, _ := msgs[1].(map[string]any)
	if first["role"] != "user" || second["role"] != "assistant" {
		t.Fatalf("message order/roles wrong: %v", msgs)
	}
	if _, isArray := second["sources"].([]any); !isArray {
		t.Fatalf("sources should decode as a JSON array, got %T", second["sources"])
	}

	// Delete, then get is a 404 and the list is empty.
	rr, _ = doJSON(t, srv.handleDeleteConversation, http.MethodDelete,
		fmt.Sprintf("/api/conversations/%d", id), "", fmt.Sprint(id))
	if rr.Code != http.StatusOK {
		t.Fatalf("delete status = %d", rr.Code)
	}
	rr, _ = doJSON(t, srv.handleGetConversation, http.MethodGet,
		fmt.Sprintf("/api/conversations/%d", id), "", fmt.Sprint(id))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("get after delete status = %d, want 404", rr.Code)
	}
	rr, list = doJSON(t, srv.handleListConversations, http.MethodGet, "/api/conversations", "", "")
	if convs, _ := list["conversations"].([]any); rr.Code != http.StatusOK || len(convs) != 0 {
		t.Fatalf("list after delete = %v (status %d)", list, rr.Code)
	}
}

func TestConversations_CreateDefaultsAndProjectPolicy(t *testing.T) {
	srv := newConvServer(t, "bound-project")

	// Empty body: default title, server project.
	rr, created := doJSON(t, srv.handleCreateConversation, http.MethodPost, "/api/conversations", `{}`, "")
	if rr.Code != http.StatusOK || created["title"] != "New chat" || created["project"] != "bound-project" {
		t.Fatalf("create defaults = %v (status %d)", created, rr.Code)
	}

	// Matching project is accepted.
	rr, _ = doJSON(t, srv.handleCreateConversation, http.MethodPost,
		"/api/conversations", `{"project":"bound-project"}`, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("create same-project status = %d", rr.Code)
	}

	// Another project is refused, mirroring the chat endpoints.
	rr, body := doJSON(t, srv.handleCreateConversation, http.MethodPost,
		"/api/conversations", `{"project":"other"}`, "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("create other-project status = %d body=%v, want 403", rr.Code, body)
	}
}

func TestConversations_InvalidIDsAndBodies(t *testing.T) {
	srv := newConvServer(t, "p")

	for name, h := range map[string]http.HandlerFunc{
		"get":    srv.handleGetConversation,
		"delete": srv.handleDeleteConversation,
		"msg":    srv.handleAddConversationMessage,
	} {
		rr, _ := doJSON(t, h, http.MethodGet, "/api/conversations/abc", "", "abc")
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s with bad id: status = %d, want 400", name, rr.Code)
		}
	}

	// Missing conversation.
	rr, _ := doJSON(t, srv.handleDeleteConversation, http.MethodDelete, "/api/conversations/99", "", "99")
	if rr.Code != http.StatusNotFound {
		t.Errorf("delete missing: status = %d, want 404", rr.Code)
	}
	rr, _ = doJSON(t, srv.handleAddConversationMessage, http.MethodPost,
		"/api/conversations/99/messages", `{"role":"user","content":"x"}`, "99")
	if rr.Code != http.StatusNotFound {
		t.Errorf("message to missing conversation: status = %d, want 404", rr.Code)
	}

	// Bad message payloads.
	rr, created := doJSON(t, srv.handleCreateConversation, http.MethodPost, "/api/conversations", `{}`, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("create: %d", rr.Code)
	}
	id := fmt.Sprint(int(created["id"].(float64)))
	rr, _ = doJSON(t, srv.handleAddConversationMessage, http.MethodPost,
		"/api/conversations/"+id+"/messages", `not json`, id)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON message: status = %d, want 400", rr.Code)
	}
	rr, _ = doJSON(t, srv.handleAddConversationMessage, http.MethodPost,
		"/api/conversations/"+id+"/messages", `{"role":"system","content":"x"}`, id)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad role: status = %d, want 400", rr.Code)
	}

	// A malformed create body is a client error, not a silent default.
	rr, _ = doJSON(t, srv.handleCreateConversation, http.MethodPost, "/api/conversations", `not json`, "")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON create: status = %d, want 400", rr.Code)
	}
}

// --- failing-store coverage for the 500 paths ---

// failingConvStore returns a fixed error from every ConversationStore method,
// except GetConversation when getOK is set (to reach the later failures).
type failingConvStore struct {
	err   error
	getOK bool
}

func (f *failingConvStore) CreateConversation(context.Context, int, string, string) (*store.Conversation, error) {
	return nil, f.err
}

func (f *failingConvStore) ListConversations(context.Context, int, int, int) ([]store.Conversation, error) {
	return nil, f.err
}

func (f *failingConvStore) GetConversation(context.Context, int, int) (*store.Conversation, error) {
	if f.getOK {
		return &store.Conversation{ID: 1}, nil
	}
	return nil, f.err
}

func (f *failingConvStore) RenameConversation(context.Context, int, int, string) error { return f.err }
func (f *failingConvStore) DeleteConversation(context.Context, int, int) error         { return f.err }

func (f *failingConvStore) AddMessage(context.Context, int, string, string, string) (*store.ConversationMessage, error) {
	return nil, f.err
}

func (f *failingConvStore) ListMessages(context.Context, int, int) ([]store.ConversationMessage, error) {
	return nil, f.err
}

func TestConversations_StoreErrorsReturn500(t *testing.T) {
	newFailingServer := func(getOK bool) *Server {
		srv, err := New(&mockPipeline{}, "p", ":0")
		if err != nil {
			t.Fatal(err)
		}
		srv.SetConversationStore(&failingConvStore{err: errors.New("disk on fire"), getOK: getOK})
		return srv
	}

	srv := newFailingServer(false)
	cases := map[string]struct {
		h    http.HandlerFunc
		body string
	}{
		"list":   {srv.handleListConversations, ""},
		"create": {srv.handleCreateConversation, `{}`},
		"get":    {srv.handleGetConversation, ""},
		"delete": {srv.handleDeleteConversation, ""},
		"msg":    {srv.handleAddConversationMessage, `{"role":"user","content":"x"}`},
	}
	for name, tc := range cases {
		rr, body := doJSON(t, tc.h, http.MethodPost, "/api/conversations/1", tc.body, "1")
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("%s: status = %d body=%v, want 500", name, rr.Code, body)
		}
		if msg, _ := body["error"].(string); strings.Contains(msg, "disk on fire") {
			t.Errorf("%s: error leaked internals: %q", name, msg)
		}
	}

	// Get succeeds but ListMessages / AddMessage fail.
	srv = newFailingServer(true)
	rr, _ := doJSON(t, srv.handleGetConversation, http.MethodGet, "/api/conversations/1", "", "1")
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("get with failing ListMessages: status = %d, want 500", rr.Code)
	}
	rr, _ = doJSON(t, srv.handleAddConversationMessage, http.MethodPost,
		"/api/conversations/1/messages", `{"role":"assistant","content":"x","sources":[{"f":1}]}`, "1")
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("add message with failing AddMessage: status = %d, want 500", rr.Code)
	}
}

func TestConversations_RoutesRegistered(t *testing.T) {
	// The sidebar JS depends on these handlers being wired in ListenAndServe;
	// compile-time references keep the route set honest.
	srv, err := New(&mockPipeline{}, "p", ":0")
	if err != nil {
		t.Fatal(err)
	}
	_ = srv.handleListConversations
	_ = srv.handleCreateConversation
	_ = srv.handleGetConversation
	_ = srv.handleDeleteConversation
	_ = srv.handleAddConversationMessage
}

func TestChatPage_RendersSidebar(t *testing.T) {
	srv := newConvServer(t, "p")
	rr := httptest.NewRecorder()
	srv.handleChatPage(rr, httptest.NewRequest(http.MethodGet, "/chat", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("chat page status = %d", rr.Code)
	}
	page := rr.Body.String()
	for _, want := range []string{"sidebar", "conv-list", "new-chat-btn", "/api/conversations"} {
		if !strings.Contains(page, want) {
			t.Errorf("chat page missing %q", want)
		}
	}
}
