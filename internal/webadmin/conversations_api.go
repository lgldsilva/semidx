package webadmin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/lgldsilva/semidx/internal/store"
)

// conversations persistence is optional (PgStore only). convStore returns the
// store as a ConversationStore, or writes a 501 and reports false.
func (a *Admin) convStore(w http.ResponseWriter) (store.ConversationStore, bool) {
	cs, ok := a.store.(store.ConversationStore)
	if !ok {
		writeJSONErr(w, http.StatusNotImplemented, "conversations are not supported by this store")
		return nil, false
	}
	return cs, true
}

func conversationJSON(c store.Conversation) map[string]any {
	return map[string]any{
		"id": c.ID, "project": c.Project, "title": c.Title,
		"created_at": c.CreatedAt, "updated_at": c.UpdatedAt,
	}
}

func messageJSON(m store.ConversationMessage) map[string]any {
	out := map[string]any{
		"id": m.ID, "role": m.Role, "content": m.Content, "created_at": m.CreatedAt,
	}
	// sources_json is stored as a JSON string; surface it as real JSON so the
	// client gets an array, not an escaped string.
	if s := strings.TrimSpace(m.SourcesJSON); s != "" {
		out["sources"] = json.RawMessage(s)
	}
	return out
}

func (a *Admin) apiListConversations(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	cs, ok := a.convStore(w)
	if !ok {
		return
	}
	convs, err := cs.ListConversations(r.Context(), ac.user.ID, 0, 0)
	if err != nil {
		a.log.Error("list conversations failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not list conversations")
		return
	}
	out := make([]map[string]any, 0, len(convs))
	for _, c := range convs {
		out = append(out, conversationJSON(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": out})
}

func (a *Admin) apiCreateConversation(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	cs, ok := a.convStore(w)
	if !ok {
		return
	}
	var body struct {
		Project string `json:"project"`
		Title   string `json:"title"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	c, err := cs.CreateConversation(r.Context(), ac.user.ID, body.Project, strings.TrimSpace(body.Title))
	if err != nil {
		a.log.Error("create conversation failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not create conversation")
		return
	}
	writeJSON(w, http.StatusOK, conversationJSON(*c))
}

func (a *Admin) apiGetConversation(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	cs, ok := a.convStore(w)
	if !ok {
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid conversation id")
		return
	}
	conv, err := cs.GetConversation(r.Context(), ac.user.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, "conversation not found")
		return
	}
	if err != nil {
		a.log.Error("get conversation failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not load conversation")
		return
	}
	msgs, err := cs.ListMessages(r.Context(), id, 0)
	if err != nil {
		a.log.Error("list messages failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not load messages")
		return
	}
	msgOut := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		msgOut = append(msgOut, messageJSON(m))
	}
	resp := conversationJSON(*conv)
	resp["messages"] = msgOut
	writeJSON(w, http.StatusOK, resp)
}

func (a *Admin) apiRenameConversation(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	cs, ok := a.convStore(w)
	if !ok {
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid conversation id")
		return
	}
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Title) == "" {
		writeJSONErr(w, http.StatusBadRequest, "title is required")
		return
	}
	switch err := cs.RenameConversation(r.Context(), ac.user.ID, id, strings.TrimSpace(body.Title)); {
	case errors.Is(err, store.ErrNotFound):
		writeJSONErr(w, http.StatusNotFound, "conversation not found")
	case err != nil:
		a.log.Error("rename conversation failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not rename conversation")
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func (a *Admin) apiDeleteConversation(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	cs, ok := a.convStore(w)
	if !ok {
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid conversation id")
		return
	}
	switch err := cs.DeleteConversation(r.Context(), ac.user.ID, id); {
	case errors.Is(err, store.ErrNotFound):
		writeJSONErr(w, http.StatusNotFound, "conversation not found")
	case err != nil:
		a.log.Error("delete conversation failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not delete conversation")
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func (a *Admin) apiAddConversationMessage(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	cs, ok := a.convStore(w)
	if !ok {
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid conversation id")
		return
	}
	var body struct {
		Role    string            `json:"role"`
		Content string            `json:"content"`
		Sources []json.RawMessage `json:"sources"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Role != "user" && body.Role != "assistant" {
		writeJSONErr(w, http.StatusBadRequest, "role must be user or assistant")
		return
	}
	// Ownership check: the conversation must belong to the caller.
	if _, err := cs.GetConversation(r.Context(), ac.user.ID, id); errors.Is(err, store.ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, "conversation not found")
		return
	} else if err != nil {
		a.log.Error("get conversation failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not load conversation")
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
		a.log.Error("add message failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not add message")
		return
	}
	writeJSON(w, http.StatusOK, messageJSON(*m))
}
