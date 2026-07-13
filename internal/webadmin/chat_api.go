package webadmin

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/lgldsilva/semidx/internal/chat"
	"github.com/lgldsilva/semidx/internal/rag"
)

type chatMessageIn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatBody struct {
	Question string          `json:"question"`
	History  []chatMessageIn `json:"history"`
}

func parseChatBody(r *http.Request) (chatBody, error) {
	var body chatBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return body, err
	}
	return body, nil
}

func historyFrom(in []chatMessageIn) []chat.Message {
	if len(in) == 0 {
		return nil
	}
	// Cap history to last 12 turns to bound token cost.
	if len(in) > 12 {
		in = in[len(in)-12:]
	}
	out := make([]chat.Message, 0, len(in))
	for _, m := range in {
		role := m.Role
		if role != "user" && role != "assistant" {
			continue
		}
		if m.Content == "" {
			continue
		}
		out = append(out, chat.Message{Role: role, Content: m.Content})
	}
	return out
}

func sourcesJSON(src []rag.Source) []map[string]any {
	out := make([]map[string]any, 0, len(src))
	for _, s := range src {
		out = append(out, map[string]any{
			"file": s.File, "start_line": s.StartLine, "end_line": s.EndLine,
			"content": s.Content, "score": s.Score, "keyword": s.Keyword, "project": s.Project,
		})
	}
	return out
}

// chatSourcesJSON is sourcesJSON for chat.Source (citations delivered on the
// terminal stream chunk by the agent path).
func chatSourcesJSON(src []chat.Source) []map[string]any {
	out := make([]map[string]any, 0, len(src))
	for _, s := range src {
		out = append(out, map[string]any{
			"file": s.File, "start_line": s.StartLine, "end_line": s.EndLine,
			"content": s.Content, "score": s.Score, "keyword": s.Keyword, "project": s.Project,
		})
	}
	return out
}

// apiProjectChat answers within one project; apiGlobalChat answers across all
// projects (empty name). Both share handleChatAsk.
func (a *Admin) apiProjectChat(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	a.handleChatAsk(w, r, r.PathValue("project"))
}

// apiGlobalChat answers a cross-project chat turn (no project binding).
func (a *Admin) apiGlobalChat(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	a.handleChatAsk(w, r, "")
}

func (a *Admin) handleChatAsk(w http.ResponseWriter, r *http.Request, name string) {
	if a.chat == nil {
		writeJSONErr(w, http.StatusServiceUnavailable, "chat is not configured — set GEMINI_API_KEY or OPENROUTER_API_KEY on the server")
		return
	}
	body, err := parseChatBody(r)
	if err != nil || body.Question == "" {
		writeJSONErr(w, http.StatusBadRequest, "question is required")
		return
	}
	ans, err := a.chat.Ask(r.Context(), body.Question, name, historyFrom(body.History))
	if err != nil {
		a.log.Error("chat ask failed", "err", err, "project", name)
		writeJSONErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"content":  ans.Content,
		"model":    ans.Model,
		"fallback": ans.Fallback,
		"keyword":  ans.Keyword,
		"sources":  sourcesJSON(ans.Sources),
	})
}

func (a *Admin) apiProjectChatStream(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	a.handleChatStream(w, r, r.PathValue("project"))
}

// apiGlobalChatStream streams a cross-project chat turn (no project binding).
func (a *Admin) apiGlobalChatStream(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	a.handleChatStream(w, r, "")
}

func (a *Admin) handleChatStream(w http.ResponseWriter, r *http.Request, name string) {
	if a.chat == nil {
		writeJSONErr(w, http.StatusServiceUnavailable, "chat is not configured — set GEMINI_API_KEY or OPENROUTER_API_KEY on the server")
		return
	}
	body, err := parseChatBody(r)
	if err != nil || body.Question == "" {
		writeJSONErr(w, http.StatusBadRequest, "question is required")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONErr(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	ch, sources, model, _, err := a.chat.StreamAsk(r.Context(), body.Question, name, historyFrom(body.History))
	if err != nil {
		a.log.Error("chat stream failed", "err", err, "project", name)
		writeJSONErr(w, http.StatusBadGateway, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Emit sources first so the UI can render citations while tokens stream.
	srcJSON, _ := json.Marshal(map[string]any{"type": "sources", "sources": sourcesJSON(sources), "model": model})
	_, _ = fmt.Fprintf(w, spaSSEDataFmt, srcJSON)
	flusher.Flush()

	for chunk := range ch {
		if chunk.Content != "" {
			tokJSON, _ := json.Marshal(map[string]any{"type": "chunk", "content": chunk.Content})
			_, _ = fmt.Fprintf(w, spaSSEDataFmt, tokJSON)
			flusher.Flush()
		}
		// Agent answers learn their sources only after the tool calls, so they
		// arrive on the terminal chunk — emit them as a (late) sources event.
		if len(chunk.Sources) > 0 {
			lateJSON, _ := json.Marshal(map[string]any{
				"type": "sources", "sources": chatSourcesJSON(chunk.Sources), "fallback": chunk.Fallback,
			})
			_, _ = fmt.Fprintf(w, spaSSEDataFmt, lateJSON)
			flusher.Flush()
		}
		if chunk.Done {
			break
		}
	}
	doneJSON, _ := json.Marshal(map[string]any{"type": "done"})
	_, _ = fmt.Fprintf(w, spaSSEDataFmt, doneJSON)
	flusher.Flush()
}
