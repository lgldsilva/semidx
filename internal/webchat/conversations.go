package webchat

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lgldsilva/semidx/internal/store"
)

// localUserID is the fixed owner of every conversation: `chatrag serve` is a
// single-user, unauthenticated local UI, so all rows belong to user 0.
const localUserID = 0

const msgConversationNotFound = "conversation not found"

// --- DTOs ---

type conversationDTO struct {
	ID        int       `json:"id"`
	Project   string    `json:"project"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type messageDTO struct {
	ID        int             `json:"id"`
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	Sources   json.RawMessage `json:"sources,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

func toConversationDTO(c store.Conversation) conversationDTO {
	return conversationDTO{ID: c.ID, Project: c.Project, Title: c.Title, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

func toMessageDTO(m store.ConversationMessage) messageDTO {
	dto := messageDTO{ID: m.ID, Role: m.Role, Content: m.Content, CreatedAt: m.CreatedAt}
	// sources_json is stored as a JSON string; surface it as real JSON so the
	// client gets an array, not an escaped string.
	if s := strings.TrimSpace(m.SourcesJSON); s != "" {
		dto.Sources = json.RawMessage(s)
	}
	return dto
}

// --- helpers ---

func (s *Server) writeConvJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set(hdrContentType, mimeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// convStore returns the conversation store, or writes a 501 and reports false
// when persistence is not wired (e.g. no local index).
func (s *Server) convStore(w http.ResponseWriter) (store.ConversationStore, bool) {
	if s.convs == nil {
		s.writeConvJSON(w, http.StatusNotImplemented,
			errorResponse{Error: "conversations are not supported by this backend"})
		return nil, false
	}
	return s.convs, true
}

// convID parses the {id} path value, or writes a 400 and reports false.
func (s *Server) convID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		s.writeConvJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid conversation id"})
		return 0, false
	}
	return id, true
}

// --- handlers ---

// handleListConversations handles GET /api/conversations.
func (s *Server) handleListConversations(w http.ResponseWriter, r *http.Request) {
	cs, ok := s.convStore(w)
	if !ok {
		return
	}
	convs, err := cs.ListConversations(r.Context(), localUserID, 0, 0)
	if err != nil {
		s.log.Error("list conversations failed", "err", err)
		s.writeConvJSON(w, http.StatusInternalServerError, errorResponse{Error: "could not list conversations"})
		return
	}
	out := make([]conversationDTO, 0, len(convs))
	for _, c := range convs {
		out = append(out, toConversationDTO(c))
	}
	s.writeConvJSON(w, http.StatusOK, map[string]any{"conversations": out})
}

// handleCreateConversation handles POST /api/conversations.
func (s *Server) handleCreateConversation(w http.ResponseWriter, r *http.Request) {
	cs, ok := s.convStore(w)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxChatBodyBytes)
	var body struct {
		Project string `json:"project"`
		Title   string `json:"title"`
	}
	// An empty body is allowed (defaults apply); any other decode error — malformed
	// JSON or a body over maxChatBodyBytes — is a client error, not a silent default.
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		s.writeConvJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	// Same project policy as the chat endpoints: a server bound to one project
	// refuses conversations for another.
	if body.Project == "" {
		body.Project = s.project
	} else if s.project != "" && body.Project != s.project {
		s.writeConvJSON(w, http.StatusForbidden, errorResponse{Error: publicChatError(errProjectNotAllowed)})
		return
	}
	c, err := cs.CreateConversation(r.Context(), localUserID, body.Project, strings.TrimSpace(body.Title))
	if err != nil {
		s.log.Error("create conversation failed", "err", err)
		s.writeConvJSON(w, http.StatusInternalServerError, errorResponse{Error: "could not create conversation"})
		return
	}
	s.writeConvJSON(w, http.StatusOK, toConversationDTO(*c))
}

// handleGetConversation handles GET /api/conversations/{id}: the conversation
// plus its messages in chronological order.
func (s *Server) handleGetConversation(w http.ResponseWriter, r *http.Request) {
	cs, ok := s.convStore(w)
	if !ok {
		return
	}
	id, ok := s.convID(w, r)
	if !ok {
		return
	}
	conv, err := cs.GetConversation(r.Context(), localUserID, id)
	if errors.Is(err, store.ErrNotFound) {
		s.writeConvJSON(w, http.StatusNotFound, errorResponse{Error: msgConversationNotFound})
		return
	}
	if err != nil {
		s.log.Error("get conversation failed", "err", err)
		s.writeConvJSON(w, http.StatusInternalServerError, errorResponse{Error: "could not load conversation"})
		return
	}
	msgs, err := cs.ListMessages(r.Context(), id, 0)
	if err != nil {
		s.log.Error("list messages failed", "err", err)
		s.writeConvJSON(w, http.StatusInternalServerError, errorResponse{Error: "could not load messages"})
		return
	}
	msgOut := make([]messageDTO, 0, len(msgs))
	for _, m := range msgs {
		msgOut = append(msgOut, toMessageDTO(m))
	}
	s.writeConvJSON(w, http.StatusOK, struct {
		conversationDTO
		Messages []messageDTO `json:"messages"`
	}{toConversationDTO(*conv), msgOut})
}

// handleDeleteConversation handles DELETE /api/conversations/{id}.
func (s *Server) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	cs, ok := s.convStore(w)
	if !ok {
		return
	}
	id, ok := s.convID(w, r)
	if !ok {
		return
	}
	switch err := cs.DeleteConversation(r.Context(), localUserID, id); {
	case errors.Is(err, store.ErrNotFound):
		s.writeConvJSON(w, http.StatusNotFound, errorResponse{Error: msgConversationNotFound})
	case err != nil:
		s.log.Error("delete conversation failed", "err", err)
		s.writeConvJSON(w, http.StatusInternalServerError, errorResponse{Error: "could not delete conversation"})
	default:
		s.writeConvJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// handleAddConversationMessage handles POST /api/conversations/{id}/messages —
// the UI persists each completed turn (user question + assistant answer) here.
func (s *Server) handleAddConversationMessage(w http.ResponseWriter, r *http.Request) {
	cs, ok := s.convStore(w)
	if !ok {
		return
	}
	id, ok := s.convID(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxChatBodyBytes)
	var body struct {
		Role    string            `json:"role"`
		Content string            `json:"content"`
		Sources []json.RawMessage `json:"sources"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeConvJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}
	if body.Role != "user" && body.Role != "assistant" {
		s.writeConvJSON(w, http.StatusBadRequest, errorResponse{Error: "role must be user or assistant"})
		return
	}
	// The conversation must exist (single-user, so this is existence, not
	// ownership) — a clearer 404 than surfacing the FK violation as a 500.
	if _, err := cs.GetConversation(r.Context(), localUserID, id); errors.Is(err, store.ErrNotFound) {
		s.writeConvJSON(w, http.StatusNotFound, errorResponse{Error: msgConversationNotFound})
		return
	} else if err != nil {
		s.log.Error("get conversation failed", "err", err)
		s.writeConvJSON(w, http.StatusInternalServerError, errorResponse{Error: "could not load conversation"})
		return
	}
	var sourcesJSON string
	if len(body.Sources) > 0 {
		if b, err := json.Marshal(body.Sources); err == nil {
			sourcesJSON = string(b)
		}
	}
	m, err := cs.AddMessage(r.Context(), id, body.Role, body.Content, sourcesJSON)
	if err != nil {
		s.log.Error("add message failed", "err", err)
		s.writeConvJSON(w, http.StatusInternalServerError, errorResponse{Error: "could not add message"})
		return
	}
	s.writeConvJSON(w, http.StatusOK, toMessageDTO(*m))
}
