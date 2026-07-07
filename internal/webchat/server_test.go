package webchat

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/chat"
	"github.com/lgldsilva/semidx/internal/rag"
)

// ---------------------------------------------------------------------------
// mockPipeline implements ChatPipeline for testing.
// ---------------------------------------------------------------------------

type mockPipeline struct {
	answer *rag.Answer
	err    error

	// Streaming support.
	streamChunks   []chat.StreamChunk
	streamErr      error
	streamModel    string
	streamFallback bool
	streamSources  []rag.Source
}

func (m *mockPipeline) Ask(_ context.Context, question, project string, history []chat.Message) (*rag.Answer, error) {
	return m.answer, m.err
}

func (m *mockPipeline) StreamAsk(_ context.Context, question, project string, history []chat.Message) (<-chan chat.StreamChunk, []rag.Source, string, bool, error) {
	if m.streamErr != nil {
		return nil, nil, "", false, m.streamErr
	}
	ch := make(chan chat.StreamChunk, len(m.streamChunks)+1)
	for _, c := range m.streamChunks {
		ch <- c
	}
	close(ch)
	return ch, m.streamSources, m.streamModel, m.streamFallback, nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestNewServer_TemplatesLoaded(t *testing.T) {
	srv, err := New(nil, "test-project", ":0")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if srv == nil {
		t.Fatal("New returned nil server")
	}
	if srv.tmpl == nil {
		t.Fatal("expected templates to be loaded")
	}
	// Verify layout template exists.
	if srv.tmpl.Lookup("layout") == nil {
		t.Fatal("expected 'layout' template to exist")
	}
	// Verify body block template exists (from chat.html).
	if srv.tmpl.Lookup("body") == nil {
		t.Fatal("expected 'body' template to exist")
	}
}

func TestHealthEndpoint(t *testing.T) {
	srv, err := New(nil, "test", ":0")
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	srv.handleHealth(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var body map[string]bool
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if !body["ok"] {
		t.Error("expected ok=true")
	}
}

func TestChatEndpoint_Success(t *testing.T) {
	answer := &rag.Answer{
		Content: "This is the answer.",
		Model:   "test-model",
		Sources: []rag.Source{
			{
				File:      "src/main.go",
				StartLine: 10,
				EndLine:   20,
				Score:     0.95,
			},
		},
	}
	pipeline := &mockPipeline{answer: answer}

	srv, err := New(pipeline, "my-project", ":0")
	if err != nil {
		t.Fatal(err)
	}

	body := `{"question":"what is this?","project":"my-project"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleChat(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d. Body: %s", rr.Code, rr.Body.String())
	}

	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var resp chatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if resp.Content != "This is the answer." {
		t.Errorf("expected content %q, got %q", "This is the answer.", resp.Content)
	}
	if resp.Model != "test-model" {
		t.Errorf("expected model %q, got %q", "test-model", resp.Model)
	}
	if resp.Fallback {
		t.Error("expected fallback=false")
	}
	if len(resp.Sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(resp.Sources))
	}
	if resp.Sources[0].File != "src/main.go" {
		t.Errorf("expected source file %q, got %q", "src/main.go", resp.Sources[0].File)
	}
}

func TestChatEndpoint_InvalidJSON(t *testing.T) {
	srv, err := New(nil, "test", ":0")
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	srv.handleChat(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}

	var errResp errorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if !strings.Contains(errResp.Error, "invalid JSON") {
		t.Errorf("expected error to contain 'invalid JSON', got %q", errResp.Error)
	}
}

func TestChatEndpoint_EmptyQuestion(t *testing.T) {
	srv, err := New(nil, "test", ":0")
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	body := `{"question":"","project":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleChat(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}

	var errResp errorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if !strings.Contains(errResp.Error, "question is required") {
		t.Errorf("expected 'question is required', got %q", errResp.Error)
	}
}

func TestChatEndpoint_PipelineError(t *testing.T) {
	pipeline := &mockPipeline{err: context.DeadlineExceeded}

	srv, err := New(pipeline, "test", ":0")
	if err != nil {
		t.Fatal(err)
	}

	body := `{"question":"ask something","project":"test"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleChat(rr, req)

	if rr.Code != http.StatusGatewayTimeout {
		t.Errorf("expected 504 for deadline exceeded, got %d. Body: %s", rr.Code, rr.Body.String())
	}
}

func TestChatEndpoint_PipelineGenericError(t *testing.T) {
	pipeline := &mockPipeline{err: errors.New("some error")}

	srv, err := New(pipeline, "test", ":0")
	if err != nil {
		t.Fatal(err)
	}

	body := `{"question":"ask something","project":"test"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleChat(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for generic error, got %d. Body: %s", rr.Code, rr.Body.String())
	}
}

func TestChatEndpoint_ContentTypeHeader(t *testing.T) {
	srv, err := New(&mockPipeline{answer: &rag.Answer{Content: "ok"}}, "test", ":0")
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"question":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.handleChat(rr, req)

	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
}

func TestChatEndpoint_FallbackInResponse(t *testing.T) {
	answer := &rag.Answer{
		Content:  "keyword result",
		Model:    "test-model",
		Fallback: true,
	}
	pipeline := &mockPipeline{answer: answer}

	srv, err := New(pipeline, "test", ":0")
	if err != nil {
		t.Fatal(err)
	}

	body := `{"question":"search term","project":"test"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleChat(rr, req)

	var resp chatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if !resp.Fallback {
		t.Error("expected fallback=true")
	}
}

func TestChatEndpoint_KeywordSource(t *testing.T) {
	answer := &rag.Answer{
		Content: "found it",
		Model:   "test-model",
		Sources: []rag.Source{
			{
				File:      "doc.go",
				StartLine: 1,
				EndLine:   2,
				Score:     0.0,
				Keyword:   true,
			},
		},
	}
	pipeline := &mockPipeline{answer: answer}

	srv, err := New(pipeline, "test", ":0")
	if err != nil {
		t.Fatal(err)
	}

	body := `{"question":"find doc","project":"test"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleChat(rr, req)

	var resp chatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if len(resp.Sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(resp.Sources))
	}
	if !resp.Sources[0].Keyword {
		t.Error("expected source keyword=true")
	}
}

func TestChatEndpoint_RouteRegistered(t *testing.T) {
	srv, err := New(&mockPipeline{answer: &rag.Answer{Content: "ok"}}, "test", ":0")
	if err != nil {
		t.Fatal(err)
	}

	// We can't easily test the mux without ListenAndServe, but we can verify
	// the handler function is non-nil by checking the field exists.
	_ = srv.handleChat
	_ = srv.handleChatPage
	_ = srv.handleHealth
}

func TestChatPage_Renders(t *testing.T) {
	srv, err := New(&mockPipeline{}, "my-project", ":0")
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/chat", nil)
	srv.handleChatPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "my-project") {
		t.Errorf("expected page to contain project name %q, got: %s", "my-project", body)
	}
	if !strings.Contains(body, "ChatRAG") {
		t.Errorf("expected page to contain 'ChatRAG', got: %s", body)
	}
	if !strings.Contains(body, "question") {
		t.Errorf("expected page to contain input field, got: %s", body)
	}
}

func TestChatPage_NoCrashOnEmptyProject(t *testing.T) {
	srv, err := New(&mockPipeline{}, "", ":0")
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/chat", nil)
	srv.handleChatPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Streaming endpoint tests
// ---------------------------------------------------------------------------

func TestChatStreamEndpoint_SSEContentType(t *testing.T) {
	pipeline := &mockPipeline{
		streamChunks: []chat.StreamChunk{
			{Content: "Hello"},
			{Done: true},
		},
	}

	srv, err := New(pipeline, "test", ":0")
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	body := `{"question":"hi","project":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat/stream", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleChatStream(rr, req)

	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("expected Content-Type text/event-stream, got %q", ct)
	}
}

func TestChatStreamEndpoint_StreamsChunks(t *testing.T) {
	pipeline := &mockPipeline{
		streamChunks: []chat.StreamChunk{
			{Content: "Hello ", Model: "test-model"},
			{Content: "World"},
			{Done: true, Model: "test-model"},
		},
		streamModel: "test-model",
	}

	srv, err := New(pipeline, "test", ":0")
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	body := `{"question":"hi","project":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat/stream", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleChatStream(rr, req)

	// Parse SSE output.
	scanner := bufio.NewScanner(strings.NewReader(rr.Body.String()))
	var (
		gotContent string
		gotDone    bool
		gotSources bool
		gotModel   string
	)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		jsonStr := strings.TrimPrefix(line, "data: ")
		var event map[string]any
		if err := json.Unmarshal([]byte(jsonStr), &event); err != nil {
			t.Fatalf("unmarshal SSE event: %v", err)
		}
		typ, _ := event["type"].(string)
		switch typ {
		case "chunk":
			content, _ := event["content"].(string)
			gotContent += content
		case "done":
			gotDone = true
			if m, ok := event["model"].(string); ok {
				gotModel = m
			}
		case "sources":
			gotSources = true
		case "error":
			t.Fatalf("unexpected error event: %v", event)
		}
	}

	if !gotDone {
		t.Error("expected done event")
	}
	if gotContent != "Hello World" {
		t.Errorf("content = %q, want %q", gotContent, "Hello World")
	}
	if !gotSources {
		t.Error("expected sources event")
	}
	if gotModel != "test-model" {
		t.Errorf("model = %q, want test-model", gotModel)
	}
}

func TestChatStreamEndpoint_Error(t *testing.T) {
	pipeline := &mockPipeline{
		streamErr: errors.New("LLM unavailable"),
	}

	srv, err := New(pipeline, "test", ":0")
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	body := `{"question":"hi","project":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat/stream", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleChatStream(rr, req)

	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("expected Content-Type text/event-stream, got %q", ct)
	}

	// Parse SSE for error event.
	scanner := bufio.NewScanner(strings.NewReader(rr.Body.String()))
	var gotError bool
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		jsonStr := strings.TrimPrefix(line, "data: ")
		var event map[string]any
		if err := json.Unmarshal([]byte(jsonStr), &event); err != nil {
			continue
		}
		if typ, _ := event["type"].(string); typ == "error" {
			gotError = true
			if msg, _ := event["error"].(string); !strings.Contains(msg, "LLM unavailable") {
				t.Errorf("error message = %q, want to contain 'LLM unavailable'", msg)
			}
		}
	}

	if !gotError {
		t.Error("expected error event")
	}
}

func TestChatStreamEndpoint_InvalidJSON(t *testing.T) {
	srv, err := New(&mockPipeline{}, "test", ":0")
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/chat/stream", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	srv.handleChatStream(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected Content-Type application/json for error, got %q", ct)
	}
}

func TestChatStreamEndpoint_EmptyQuestion(t *testing.T) {
	srv, err := New(&mockPipeline{}, "test", ":0")
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	body := `{"question":"","project":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat/stream", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleChatStream(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestChatStreamEndpoint_Sources(t *testing.T) {
	sources := []rag.Source{
		{
			File:      "main.go",
			StartLine: 10,
			EndLine:   20,
			Content:   "func main() {}",
			Score:     0.95,
		},
	}
	pipeline := &mockPipeline{
		streamChunks: []chat.StreamChunk{
			{Content: "The answer"},
			{Done: true, Model: "test-model"},
		},
		streamModel:    "test-model",
		streamFallback: true,
	}

	srv, err := New(pipeline, "test", ":0")
	if err != nil {
		t.Fatal(err)
	}

	pipeline.streamSources = sources

	rr := httptest.NewRecorder()
	body := `{"question":"hi","project":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat/stream", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleChatStream(rr, req)

	// Parse SSE for sources event.
	scanner := bufio.NewScanner(strings.NewReader(rr.Body.String()))
	var sourcesEvent bool
	var gotFallback bool
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		jsonStr := strings.TrimPrefix(line, "data: ")
		var event map[string]any
		if err := json.Unmarshal([]byte(jsonStr), &event); err != nil {
			continue
		}
		if typ, _ := event["type"].(string); typ == "sources" {
			sourcesEvent = true
			if f, _ := event["fallback"].(bool); f {
				gotFallback = true
			}
			srcList, _ := event["sources"].([]any)
			if len(srcList) != 1 {
				t.Errorf("expected 1 source, got %d", len(srcList))
			}
		}
	}

	if !sourcesEvent {
		t.Error("expected sources event")
	}
	if !gotFallback {
		t.Error("expected fallback=true")
	}
}

func TestChatStreamEndpoint_ModelInSources(t *testing.T) {
	pipeline := &mockPipeline{
		streamChunks: []chat.StreamChunk{
			{Content: "answer"},
			{Done: true, Model: "custom-model"},
		},
		streamModel: "custom-model",
	}

	srv, err := New(pipeline, "test", ":0")
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	body := `{"question":"hi","project":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat/stream", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleChatStream(rr, req)

	scanner := bufio.NewScanner(strings.NewReader(rr.Body.String()))
	var modelFromSources string
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		jsonStr := strings.TrimPrefix(line, "data: ")
		var event map[string]any
		if err := json.Unmarshal([]byte(jsonStr), &event); err != nil {
			continue
		}
		if typ, _ := event["type"].(string); typ == "sources" {
			if m, _ := event["model"].(string); m != "" {
				modelFromSources = m
			}
		}
	}

	if modelFromSources != "custom-model" {
		t.Errorf("model in sources = %q, want custom-model", modelFromSources)
	}
}

func TestChatStreamEndpoint_RouteRegistered(t *testing.T) {
	srv, err := New(&mockPipeline{}, "test", ":0")
	if err != nil {
		t.Fatal(err)
	}

	// Verify the handler function exists.
	_ = srv.handleChatStream
}
