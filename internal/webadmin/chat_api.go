package webadmin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

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
	// Mode/Model optionally override the server defaults for this turn; both
	// are validated against the pipeline's Config() (unknown values → 400).
	Mode  string `json:"mode"`
	Model string `json:"model"`
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

// chatUnavailableMsg is the 503 body when no chat pipeline is wired. It must
// guide the user: some providers (OpenRouter, Groq) have no default model, so a
// bare API key without SEMIDX_CHAT_MODEL still leaves chat disabled.
const chatUnavailableMsg = "chat is not configured: set a chat provider key — and SEMIDX_CHAT_MODEL for providers without a default model (OpenRouter, Groq) — on the server (see semidx config list)"

// chatOptionsFrom validates the requested mode/model against the pipeline's
// advertised config and returns the per-request options. Empty fields mean the
// server defaults and always pass.
func chatOptionsFrom(cfg ChatConfig, body chatBody) (ChatOptions, error) {
	opts := ChatOptions{Mode: body.Mode, Model: body.Model}
	if opts.Mode != "" && !slices.Contains(cfg.Modes, opts.Mode) {
		return opts, fmt.Errorf("unknown chat mode %q (modes: %s)", opts.Mode, strings.Join(cfg.Modes, ", "))
	}
	if opts.Model != "" && !slices.ContainsFunc(cfg.Models, func(m ChatModelInfo) bool { return m.ID == opts.Model }) {
		return opts, fmt.Errorf("model %q is not in the chat allowlist (see GET /admin/api/chat/config)", opts.Model)
	}
	return opts, nil
}

// apiChatConfig reports the chat capability contract for the SPA selector.
// 404 when chat is not configured — the frontend hides the selector on 404 or
// enabled:false.
func (a *Admin) apiChatConfig(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_, _ = r, ac
	if a.chat == nil {
		writeJSONErr(w, http.StatusNotFound, "chat is not configured")
		return
	}
	writeJSON(w, http.StatusOK, a.chat.Config())
}

func (a *Admin) handleChatAsk(w http.ResponseWriter, r *http.Request, name string) {
	if a.chat == nil {
		writeJSONErr(w, http.StatusServiceUnavailable, chatUnavailableMsg)
		return
	}
	body, err := parseChatBody(r)
	if err != nil || body.Question == "" {
		writeJSONErr(w, http.StatusBadRequest, "question is required")
		return
	}
	opts, err := chatOptionsFrom(a.chat.Config(), body)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ans, err := a.chat.Ask(r.Context(), body.Question, name, historyFrom(body.History), opts)
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
		writeJSONErr(w, http.StatusServiceUnavailable, chatUnavailableMsg)
		return
	}
	// Check Flusher before reading the body so a non-stream fallback can still
	// parse the request (instrument() historically broke Flusher promotion).
	flusher, ok := w.(http.Flusher)
	if !ok {
		a.handleChatAsk(w, r, name)
		return
	}
	body, err := parseChatBody(r)
	if err != nil || body.Question == "" {
		writeJSONErr(w, http.StatusBadRequest, "question is required")
		return
	}
	opts, err := chatOptionsFrom(a.chat.Config(), body)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}

	ch, sources, model, _, err := a.chat.StreamAsk(r.Context(), body.Question, name, historyFrom(body.History), opts)
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
		// Tool activity (agent streams): emit tool_call / tool_result events so
		// the UI can show the loop working before tokens arrive.
		if chunk.Tool != nil {
			if evJSON := toolEventJSON(chunk.Tool); evJSON != nil {
				_, _ = fmt.Fprintf(w, spaSSEDataFmt, evJSON)
				flusher.Flush()
			}
		}
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
		// A failed stream surfaces a sanitized error event before done — the
		// message is generic by contract (the real error is server-log only).
		if chunk.Err != "" {
			errJSON, _ := json.Marshal(map[string]any{"type": "error", "message": chunk.Err})
			_, _ = fmt.Fprintf(w, spaSSEDataFmt, errJSON)
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

// toolEventJSON renders one mid-stream tool event per the frozen SSE contract:
// tool_call carries the sanitized args object, tool_result a bounded preview.
// Unknown kinds yield nil (skipped).
func toolEventJSON(ev *chat.ToolEvent) []byte {
	switch ev.Kind {
	case chat.ToolEventCall:
		args := ev.Args
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		b, _ := json.Marshal(map[string]any{
			"type": "tool_call", "id": ev.ID, "name": ev.Name, "args": args,
		})
		return b
	case chat.ToolEventResult:
		b, _ := json.Marshal(map[string]any{
			"type": "tool_result", "id": ev.ID, "name": ev.Name,
			"preview": ev.Preview, "is_error": ev.IsError,
			"elapsed_ms": ev.ElapsedMS, "truncated": ev.Truncated,
		})
		return b
	}
	return nil
}
