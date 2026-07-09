package webchat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/lgldsilva/semidx/internal/chat"
	"github.com/lgldsilva/semidx/internal/rag"
)

// SSE event type constants.
const (
	sseTypeError   = "error"
	sseTypeDone    = "done"
	sseTypeChunk   = "chunk"
	sseTypeSources = "sources"
)

const hdrContentType = "Content-Type"
const mimeJSON = "application/json"

const errQuestionRequired = "question is required"
const sseDataFmt = "data: %s\n\n"

// jsonKey constants used in SSE payloads.
const (
	jsonKeyType    = "type"
	jsonKeyError   = "error"
	jsonKeyModel   = "model"
	jsonKeyContent = "content"
	jsonKeySources = "sources"
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
	w.Header().Set(hdrContentType, mimeJSON)

	req, err := s.decodeChatRequest(w, r)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(errorResponse{Error: "invalid JSON"})
		return
	}

	if req.Question == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(errorResponse{Error: errQuestionRequired})
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
	req, project, err := s.validateChatStreamRequest(w, r)
	if err != nil {
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.handleChat(w, r)
		return
	}
	w.Header().Set(hdrContentType, "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	chunks, sources, modelName, fallback, err := s.pipeline.StreamAsk(ctx, req.Question, project, req.History)
	if err != nil {
		s.log.Error("stream ask failed", "err", err)
		errJSON, _ := json.Marshal(map[string]string{jsonKeyType: sseTypeError, jsonKeyError: publicChatError(err)})
		_, _ = fmt.Fprintf(w, sseDataFmt, errJSON)
		flusher.Flush()
		return
	}

	resolvedModel := s.streamChatChunks(ctx, w, flusher, chunks, modelName)
	s.sendChatSources(w, flusher, sources, resolvedModel, fallback)
}

func (s *Server) validateChatStreamRequest(w http.ResponseWriter, r *http.Request) (chatRequest, string, error) {
	req, err := s.decodeChatRequest(w, r)
	if err != nil {
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(errorResponse{Error: "invalid JSON"})
		return chatRequest{}, "", err
	}

	if req.Question == "" {
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(errorResponse{Error: errQuestionRequired})
		return chatRequest{}, "", errors.New(errQuestionRequired)
	}

	project, err := s.resolveProject(req.Project)
	if err != nil {
		w.Header().Set(hdrContentType, mimeJSON)
		status := http.StatusBadRequest
		if errors.Is(err, errProjectNotAllowed) {
			status = http.StatusForbidden
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(errorResponse{Error: publicChatError(err)})
		return chatRequest{}, "", err
	}

	return req, project, nil
}

func (s *Server) streamChatChunks(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, chunks <-chan chat.StreamChunk, modelName string) string {
	var resolvedModel string
	for {
		select {
		case <-ctx.Done():
			return resolvedModel
		case chunk, ok := <-chunks:
			if !ok {
				resolvedModel = s.resolveModel(resolvedModel, "", modelName)
				s.sendSSE(w, flusher, map[string]any{jsonKeyType: sseTypeDone, jsonKeyModel: resolvedModel})
				return resolvedModel
			}

			if chunk.Content != "" {
				if chunk.Model != "" {
					resolvedModel = chunk.Model
				}
				s.sendSSE(w, flusher, map[string]any{jsonKeyType: sseTypeChunk, jsonKeyContent: chunk.Content})
			}

			if chunk.Done {
				resolvedModel = s.resolveModel(resolvedModel, chunk.Model, modelName)
				s.sendSSE(w, flusher, map[string]any{jsonKeyType: sseTypeDone, jsonKeyModel: resolvedModel})
				return resolvedModel
			}
		}
	}
}

func (s *Server) sendSSE(w http.ResponseWriter, flusher http.Flusher, payload map[string]any) {
	data, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, sseDataFmt, data)
	flusher.Flush()
}

func (s *Server) resolveModel(current, chunkModel, defaultModel string) string {
	if chunkModel != "" {
		return chunkModel
	}
	if current != "" {
		return current
	}
	return defaultModel
}

func (s *Server) sendChatSources(w http.ResponseWriter, flusher http.Flusher, sources []rag.Source, resolvedModel string, fallback bool) {
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
		jsonKeyType:    sseTypeSources,
		jsonKeySources: srcEntries,
		jsonKeyModel:   resolvedModel,
		"fallback":     fallback,
	})
	_, _ = fmt.Fprintf(w, sseDataFmt, sourcesJSON)
	flusher.Flush()
}

// handleHealth handles GET /api/health.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(hdrContentType, mimeJSON)
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
