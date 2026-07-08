package webchat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/lgldsilva/semidx/internal/chat"
)

// --- Request/response types ---

// chatRequest is the JSON body for POST /api/chat.
type chatRequest struct {
	Question string         `json:"question"`
	Project  string         `json:"project"`
	History  []chat.Message `json:"history,omitempty"`
}

// chatResponse is the JSON response for a successful chat request.
type chatResponse struct {
	Content  string        `json:"content"`
	Sources  []sourceEntry `json:"sources,omitempty"`
	Model    string        `json:"model"`
	Fallback bool          `json:"fallback"`
}

// sourceEntry is a single source entry in the chat response.
type sourceEntry struct {
	File      string  `json:"file"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Content   string  `json:"content,omitempty"`
	Score     float64 `json:"score"`
	Keyword   bool    `json:"keyword"`
}

// errorResponse is the JSON error body.
type errorResponse struct {
	Error string `json:"error"`
}

// --- Handlers ---

// handleChatPage renders the chat HTML page.
func (s *Server) handleChatPage(w http.ResponseWriter, r *http.Request) {
	data := pageData{Project: s.project}
	if err := s.tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		s.log.Error("template execution failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// handleChat handles POST /api/chat requests.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	req, err := s.decodeChatRequest(w, r)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(errorResponse{Error: "invalid JSON"})
		return
	}

	if req.Question == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(errorResponse{Error: "question is required"})
		return
	}

	project, err := s.resolveProject(req.Project)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errProjectNotAllowed) {
			status = http.StatusForbidden
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(errorResponse{Error: publicChatError(err)})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	answer, err := s.pipeline.Ask(ctx, req.Question, project, req.History)
	if err != nil {
		s.log.Error("pipeline ask failed", "err", err)
		status := http.StatusInternalServerError
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			status = http.StatusGatewayTimeout
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(errorResponse{Error: publicChatError(err)})
		return
	}

	sources := make([]sourceEntry, len(answer.Sources))
	for i, src := range answer.Sources {
		sources[i] = sourceEntry{
			File:      src.File,
			StartLine: src.StartLine,
			EndLine:   src.EndLine,
			Content:   src.Content,
			Score:     src.Score,
			Keyword:   src.Keyword,
		}
	}

	resp := chatResponse{
		Content:  answer.Content,
		Sources:  sources,
		Model:    answer.Model,
		Fallback: answer.Fallback,
	}

	_ = json.NewEncoder(w).Encode(resp)
}

// handleChatStream handles POST /api/chat/stream with SSE streaming.
func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	req, err := s.decodeChatRequest(w, r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(errorResponse{Error: "invalid JSON"})
		return
	}

	if req.Question == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(errorResponse{Error: "question is required"})
		return
	}

	project, err := s.resolveProject(req.Project)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusBadRequest
		if errors.Is(err, errProjectNotAllowed) {
			status = http.StatusForbidden
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(errorResponse{Error: publicChatError(err)})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	// Set SSE headers before calling the pipeline.
	flusher, ok := w.(http.Flusher)
	if !ok {
		// If the ResponseWriter doesn't support flushing, fall back to JSON.
		s.handleChat(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	chunks, sources, modelName, fallback, err := s.pipeline.StreamAsk(ctx, req.Question, project, req.History)
	if err != nil {
		s.log.Error("stream ask failed", "err", err)
		errJSON, _ := json.Marshal(map[string]string{"type": "error", "error": publicChatError(err)})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", errJSON)
		flusher.Flush()
		return
	}

	// Stream chunks as SSE events.
	var resolvedModel string
	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-chunks:
			if !ok {
				// Channel closed without Done — treat as done.
				if resolvedModel == "" {
					resolvedModel = modelName
				}
				doneJSON, _ := json.Marshal(map[string]any{"type": "done", "model": resolvedModel})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", doneJSON)
				flusher.Flush()
				goto sendSources
			}

			if chunk.Content != "" {
				if chunk.Model != "" {
					resolvedModel = chunk.Model
				}
				chunkJSON, _ := json.Marshal(map[string]any{"type": "chunk", "content": chunk.Content})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", chunkJSON)
				flusher.Flush()
			}

			if chunk.Done {
				if chunk.Model != "" {
					resolvedModel = chunk.Model
				}
				if resolvedModel == "" {
					resolvedModel = modelName
				}
				doneJSON, _ := json.Marshal(map[string]any{"type": "done", "model": resolvedModel})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", doneJSON)
				flusher.Flush()
				goto sendSources
			}
		}
	}

sendSources:
	// Send sources as a final SSE event.
	srcEntries := make([]sourceEntry, len(sources))
	for i, src := range sources {
		srcEntries[i] = sourceEntry{
			File:      src.File,
			StartLine: src.StartLine,
			EndLine:   src.EndLine,
			Content:   src.Content,
			Score:     src.Score,
			Keyword:   src.Keyword,
		}
	}
	sourcesJSON, _ := json.Marshal(map[string]any{
		"type":     "sources",
		"sources":  srcEntries,
		"model":    resolvedModel,
		"fallback": fallback,
	})
	_, _ = fmt.Fprintf(w, "data: %s\n\n", sourcesJSON)
	flusher.Flush()
}

// handleHealth handles GET /api/health.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
