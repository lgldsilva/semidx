package webadmin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lgldsilva/semidx/internal/chat"
	"github.com/lgldsilva/semidx/internal/rag"
)

type fakeChat struct{}

func (fakeChat) Ask(_ context.Context, _ string, _ string, _ []chat.Message, _ ChatOptions) (*rag.Answer, error) {
	return &rag.Answer{
		Content: "answer",
		Model:   "test-model",
		Sources: []rag.Source{{File: "a.go", StartLine: 1, EndLine: 2, Content: "x", Score: 0.9}},
	}, nil
}

func (fakeChat) Config() ChatConfig {
	return ChatConfig{
		Enabled:      true,
		Modes:        []string{"agent", "rag"},
		DefaultMode:  "agent",
		DefaultModel: "test-model",
		Models: []ChatModelInfo{
			{ID: "test-model", Provider: "fake", Default: true},
			{ID: "other-model", Provider: "fake"},
		},
		AgentActions: "off",
	}
}

func (fakeChat) StreamAsk(_ context.Context, _ string, _ string, _ []chat.Message, _ ChatOptions) (<-chan chat.StreamChunk, []rag.Source, string, bool, error) {
	ch := make(chan chat.StreamChunk, 4)
	ch <- chat.StreamChunk{Tool: &chat.ToolEvent{
		Kind: chat.ToolEventCall, ID: "call_abc", Name: "semantic_search",
		Args: json.RawMessage(`{"query":"auth","top_k":5}`),
	}}
	ch <- chat.StreamChunk{Tool: &chat.ToolEvent{
		Kind: chat.ToolEventResult, ID: "call_abc", Name: "semantic_search",
		Preview: "match preview", ElapsedMS: 412, Truncated: true,
	}}
	ch <- chat.StreamChunk{Content: "hi"}
	ch <- chat.StreamChunk{Done: true}
	close(ch)
	return ch, []rag.Source{{File: "a.go", StartLine: 1, EndLine: 1}}, "test-model", false, nil
}

func TestHistoryFrom(t *testing.T) {
	got := historyFrom([]chatMessageIn{
		{Role: "user", Content: "q"},
		{Role: "assistant", Content: "a"},
		{Role: "system", Content: "skip"},
	})
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	// History cap keeps only the last 12 turns.
	long := make([]chatMessageIn, 15)
	for i := range long {
		long[i] = chatMessageIn{Role: "user", Content: "x"}
	}
	if len(historyFrom(long)) != 12 {
		t.Fatal("expected history cap at 12")
	}
}

func TestChatAPI(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	a, _ := New(fs, fakeEmbedder{}, nil, true, nil, "")
	a.SetChat(fakeChat{})
	srv.Config.Handler = a.Handler()

	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/projects/demo/chat", csrf, map[string]any{
		"question": "what is main?",
	})
	if code != 200 || !strings.Contains(body, `"content":"answer"`) {
		t.Fatalf("chat = %d body=%s", code, body)
	}
}

func TestChatStreamAPI(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	a, _ := New(fs, fakeEmbedder{}, nil, true, nil, "")
	a.SetChat(fakeChat{})
	srv.Config.Handler = a.Handler()

	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/admin/api/projects/demo/chat/stream", strings.NewReader(`{"question":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream = %d ct=%s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

// TestGlobalChatAPI exercises the project-less (cross-project) chat endpoints.
func TestGlobalChatAPI(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	a, _ := New(fs, fakeEmbedder{}, nil, true, nil, "")
	a.SetChat(fakeChat{})
	srv.Config.Handler = a.Handler()

	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/chat", csrf, map[string]any{
		"question": "how does auth work across projects?",
	})
	if code != 200 || !strings.Contains(body, `"content":"answer"`) {
		t.Fatalf("global chat = %d body=%s", code, body)
	}
	if !strings.Contains(body, `"project"`) {
		t.Errorf("sources should carry a project field: %s", body)
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/admin/api/chat/stream", strings.NewReader(`{"question":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("global stream = %d ct=%s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func TestChatAPINoConfig(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/projects/demo/chat", csrf, map[string]any{"question": "x"})
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "not configured") {
		t.Fatalf("no chat = %d body=%s", code, body)
	}
}

type errChat struct{ fakeChat }

func (errChat) Ask(context.Context, string, string, []chat.Message, ChatOptions) (*rag.Answer, error) {
	return nil, errors.New("upstream failed")
}
func (errChat) StreamAsk(context.Context, string, string, []chat.Message, ChatOptions) (<-chan chat.StreamChunk, []rag.Source, string, bool, error) {
	return nil, nil, "", false, errors.New("upstream failed")
}

func TestChatAPIBadRequest(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	a, _ := New(fs, fakeEmbedder{}, nil, true, nil, "")
	a.SetChat(fakeChat{})
	srv.Config.Handler = a.Handler()
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/projects/demo/chat", csrf, map[string]any{"question": ""})
	if code != 400 || !strings.Contains(body, "question is required") {
		t.Fatalf("empty question = %d body=%s", code, body)
	}
}

func TestChatAPIUpstreamError(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	a, _ := New(fs, fakeEmbedder{}, nil, true, nil, "")
	a.SetChat(errChat{})
	srv.Config.Handler = a.Handler()
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/projects/demo/chat", csrf, map[string]any{"question": "x"})
	if code != 502 || !strings.Contains(body, "upstream failed") {
		t.Fatalf("upstream error = %d body=%s", code, body)
	}
}

// nonFlusherRW deliberately does not implement http.Flusher.
type nonFlusherRW struct{ http.ResponseWriter }

func TestChatStreamFallsBackWhenNoFlusher(t *testing.T) {
	fs := newFakeStore()
	fs.addUser("admin", "supersecret", "admin")
	a, err := New(fs, fakeEmbedder{}, nil, true, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	a.SetChat(fakeChat{})

	// Call handleChatStream with a writer that has no Flush — must degrade to JSON Ask.
	body := strings.NewReader(`{"question":"hi"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/projects/demo/chat/stream", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	a.handleChatStream(&nonFlusherRW{rr}, req, "demo")
	if rr.Code != 200 {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"content":"answer"`) {
		t.Fatalf("expected non-stream fallback body, got %s", rr.Body.String())
	}
	if strings.Contains(rr.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatal("fallback must not use event-stream")
	}
}

// sseEvents fetches an SSE chat stream and decodes every data frame in order.
func sseEvents(t *testing.T, c *http.Client, url, csrf string) []map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"question":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("stream = %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var events []map[string]any
	for _, frame := range strings.Split(string(raw), "\n\n") {
		frame = strings.TrimSpace(frame)
		if frame == "" {
			continue
		}
		data, ok := strings.CutPrefix(frame, "data: ")
		if !ok {
			t.Fatalf("unexpected SSE frame: %q", frame)
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			t.Fatalf("bad event JSON %q: %v", data, err)
		}
		events = append(events, ev)
	}
	return events
}

// eventTypes projects the "type" field of each event, in order.
func eventTypes(events []map[string]any) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i], _ = ev["type"].(string)
	}
	return out
}

// TestChatStreamToolEvents asserts the frozen SSE contract ordering —
// sources → tool_call → tool_result → chunk → done — and the tool payloads.
func TestChatStreamToolEvents(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	a, _ := New(fs, fakeEmbedder{}, nil, true, nil, "")
	a.SetChat(fakeChat{})
	srv.Config.Handler = a.Handler()

	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	events := sseEvents(t, c, srv.URL+"/admin/api/projects/demo/chat/stream", csrf)
	got := eventTypes(events)
	want := []string{"sources", "tool_call", "tool_result", "chunk", "done"}
	if len(got) != len(want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event[%d] = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}

	call := events[1]
	if call["id"] != "call_abc" || call["name"] != "semantic_search" {
		t.Errorf("tool_call payload = %v", call)
	}
	args, ok := call["args"].(map[string]any)
	if !ok || args["query"] != "auth" {
		t.Errorf("tool_call args must be the sanitized JSON object, got %v", call["args"])
	}

	res := events[2]
	if res["id"] != "call_abc" || res["name"] != "semantic_search" {
		t.Errorf("tool_result payload = %v", res)
	}
	if res["preview"] != "match preview" || res["is_error"] != false ||
		res["elapsed_ms"] != float64(412) || res["truncated"] != true {
		t.Errorf("tool_result fields = %v", res)
	}
}

// failedStreamChat ends its stream with a terminal chunk carrying a sanitized
// error message (the agent path when runner.Stream fails mid-flight).
type failedStreamChat struct{ fakeChat }

func (failedStreamChat) StreamAsk(context.Context, string, string, []chat.Message, ChatOptions) (<-chan chat.StreamChunk, []rag.Source, string, bool, error) {
	ch := make(chan chat.StreamChunk, 2)
	ch <- chat.StreamChunk{Content: "partial"}
	ch <- chat.StreamChunk{Done: true, Err: "chat backend failed — check server logs"}
	close(ch)
	return ch, nil, "test-model", false, nil
}

// TestChatStreamErrorEvent asserts a failed stream emits the error event with
// the generic message BEFORE the done event.
func TestChatStreamErrorEvent(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	a, _ := New(fs, fakeEmbedder{}, nil, true, nil, "")
	a.SetChat(failedStreamChat{})
	srv.Config.Handler = a.Handler()

	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	events := sseEvents(t, c, srv.URL+"/admin/api/projects/demo/chat/stream", csrf)
	types := eventTypes(events)
	errIdx, doneIdx := -1, -1
	for i, typ := range types {
		if typ == "error" && errIdx == -1 {
			errIdx = i
		}
		if typ == "done" {
			doneIdx = i
		}
	}
	if errIdx == -1 || doneIdx == -1 || errIdx >= doneIdx {
		t.Fatalf("want error before done, got %v", types)
	}
	if msg, _ := events[errIdx]["message"].(string); msg != "chat backend failed — check server logs" {
		t.Errorf("error message = %q, want the generic contract message", msg)
	}
}

// TestChatOptionsFrom covers the pure validation: empty fields always pass,
// listed values pass, anything else is rejected.
func TestChatOptionsFrom(t *testing.T) {
	cfg := fakeChat{}.Config()
	if _, err := chatOptionsFrom(cfg, chatBody{}); err != nil {
		t.Errorf("empty opts must pass: %v", err)
	}
	opts, err := chatOptionsFrom(cfg, chatBody{Mode: "rag", Model: "other-model"})
	if err != nil || opts.Mode != "rag" || opts.Model != "other-model" {
		t.Errorf("valid opts = %+v err=%v", opts, err)
	}
	if _, err := chatOptionsFrom(cfg, chatBody{Mode: "bogus"}); err == nil {
		t.Error("unknown mode must be rejected")
	}
	if _, err := chatOptionsFrom(cfg, chatBody{Model: "not-listed"}); err == nil {
		t.Error("model outside the allowlist must be rejected")
	}
}

// TestChatConfigAPI asserts the frozen GET /admin/api/chat/config contract keys.
func TestChatConfigAPI(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	a, _ := New(fs, fakeEmbedder{}, nil, true, nil, "")
	a.SetChat(fakeChat{})
	srv.Config.Handler = a.Handler()

	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	resp, err := c.Get(srv.URL + "/admin/api/chat/config")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("config = %d", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["enabled"] != true || got["default_mode"] != "agent" ||
		got["default_model"] != "test-model" || got["agent_actions"] != "off" {
		t.Errorf("config = %v", got)
	}
	modes, _ := got["modes"].([]any)
	if len(modes) != 2 || modes[0] != "agent" || modes[1] != "rag" {
		t.Errorf("modes = %v", got["modes"])
	}
	models, _ := got["models"].([]any)
	if len(models) != 2 {
		t.Fatalf("models = %v", got["models"])
	}
	first, _ := models[0].(map[string]any)
	if first["id"] != "test-model" || first["provider"] != "fake" || first["default"] != true {
		t.Errorf("models[0] = %v", first)
	}
}

// TestChatConfigAPINotConfigured: without a chat pipeline the endpoint answers
// 404 (the frontend hides the selector).
func TestChatConfigAPINotConfigured(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	resp, err := c.Get(srv.URL + "/admin/api/chat/config")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("config without chat = %d, want 404", resp.StatusCode)
	}
}

// TestChatAPIRejectsBadModeModel: mode/model outside the advertised config must
// yield 400 on both the ask and stream endpoints.
func TestChatAPIRejectsBadModeModel(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	a, _ := New(fs, fakeEmbedder{}, nil, true, nil, "")
	a.SetChat(fakeChat{})
	srv.Config.Handler = a.Handler()

	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/projects/demo/chat", csrf, map[string]any{
		"question": "x", "mode": "bogus",
	})
	if code != 400 || !strings.Contains(body, "unknown chat mode") {
		t.Fatalf("bad mode = %d body=%s", code, body)
	}
	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/projects/demo/chat", csrf, map[string]any{
		"question": "x", "model": "not-listed",
	})
	if code != 400 || !strings.Contains(body, "not in the chat allowlist") {
		t.Fatalf("bad model = %d body=%s", code, body)
	}
	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/chat/stream", csrf, map[string]any{
		"question": "x", "model": "not-listed",
	})
	if code != 400 || !strings.Contains(body, "not in the chat allowlist") {
		t.Fatalf("stream bad model = %d body=%s", code, body)
	}
}

// recordingChat captures the ChatOptions each call received.
type recordingChat struct {
	fakeChat
	mu   sync.Mutex
	last ChatOptions
}

func (r *recordingChat) record(opts ChatOptions) {
	r.mu.Lock()
	r.last = opts
	r.mu.Unlock()
}

func (r *recordingChat) opts() ChatOptions {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

func (r *recordingChat) Ask(ctx context.Context, q, p string, h []chat.Message, opts ChatOptions) (*rag.Answer, error) {
	r.record(opts)
	return r.fakeChat.Ask(ctx, q, p, h, opts)
}

func (r *recordingChat) StreamAsk(ctx context.Context, q, p string, h []chat.Message, opts ChatOptions) (<-chan chat.StreamChunk, []rag.Source, string, bool, error) {
	r.record(opts)
	return r.fakeChat.StreamAsk(ctx, q, p, h, opts)
}

// TestChatAPIPassesOptions: validated mode/model reach the pipeline untouched.
func TestChatAPIPassesOptions(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	a, _ := New(fs, fakeEmbedder{}, nil, true, nil, "")
	rec := &recordingChat{}
	a.SetChat(rec)
	srv.Config.Handler = a.Handler()

	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/projects/demo/chat", csrf, map[string]any{
		"question": "x", "mode": "rag", "model": "other-model",
	})
	if code != 200 {
		t.Fatalf("chat = %d body=%s", code, body)
	}
	if got := rec.opts(); got.Mode != "rag" || got.Model != "other-model" {
		t.Errorf("pipeline received opts %+v", got)
	}
}

func TestChatStreamUpstreamError(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	a, _ := New(fs, fakeEmbedder{}, nil, true, nil, "")
	a.SetChat(errChat{})
	srv.Config.Handler = a.Handler()
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/admin/api/projects/demo/chat/stream", strings.NewReader(`{"question":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 502 {
		t.Fatalf("stream upstream = %d", resp.StatusCode)
	}
}

// coverage-patch: 2026-07-17
func TestParseChatBody(t *testing.T) {
	t.Run("valid json", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"question":"hello","history":[{"role":"user","content":"hi"}]}`))
		body, err := parseChatBody(r)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if body.Question != "hello" {
			t.Errorf("question = %q; want hello", body.Question)
		}
		if len(body.History) != 1 || body.History[0].Role != "user" {
			t.Errorf("history = %+v", body.History)
		}
	})
	t.Run("invalid json", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{not json}`))
		_, err := parseChatBody(r)
		if err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})
	t.Run("empty body", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
		body, err := parseChatBody(r)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if body.Question != "" {
			t.Errorf("question = %q; want empty", body.Question)
		}
	})
}

// coverage-patch: 2026-07-17
func TestToolEventJSON(t *testing.T) {
	t.Run("call event", func(t *testing.T) {
		ev := &chat.ToolEvent{Kind: chat.ToolEventCall, ID: "call_1", Name: "search", Args: json.RawMessage(`{"q":"test"}`)}
		b := toolEventJSON(ev)
		if b == nil {
			t.Fatal("got nil")
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		if m["type"] != "tool_call" || m["id"] != "call_1" || m["name"] != "search" {
			t.Errorf("unexpected call event: %+v", m)
		}
	})
	t.Run("call event nil args", func(t *testing.T) {
		ev := &chat.ToolEvent{Kind: chat.ToolEventCall, ID: "call_2", Name: "search"}
		b := toolEventJSON(ev)
		if b == nil {
			t.Fatal("got nil")
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		args, ok := m["args"].(map[string]any)
		if !ok || len(args) != 0 {
			t.Errorf("expected empty args object, got %v", m["args"])
		}
	})
	t.Run("result event", func(t *testing.T) {
		ev := &chat.ToolEvent{Kind: chat.ToolEventResult, ID: "call_1", Name: "search", Preview: "found 3 results", IsError: false, ElapsedMS: 150, Truncated: false}
		b := toolEventJSON(ev)
		if b == nil {
			t.Fatal("got nil")
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		if m["type"] != "tool_result" || m["id"] != "call_1" || m["preview"] != "found 3 results" {
			t.Errorf("unexpected result event: %+v", m)
		}
		if m["is_error"] != false {
			t.Errorf("is_error = %v; want false", m["is_error"])
		}
	})
	t.Run("result event with error", func(t *testing.T) {
		ev := &chat.ToolEvent{Kind: chat.ToolEventResult, ID: "call_3", Name: "search", Preview: "failed", IsError: true, ElapsedMS: 200, Truncated: true}
		b := toolEventJSON(ev)
		if b == nil {
			t.Fatal("got nil")
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		if m["is_error"] != true || m["truncated"] != true || m["elapsed_ms"] != float64(200) {
			t.Errorf("unexpected result with error: %+v", m)
		}
	})
	t.Run("unknown kind", func(t *testing.T) {
		ev := &chat.ToolEvent{Kind: "unknown"}
		b := toolEventJSON(ev)
		if b != nil {
			t.Errorf("expected nil for unknown kind, got %s", string(b))
		}
	})
}

// coverage-patch: 2026-07-17
func TestChatSourcesJSON(t *testing.T) {
	tests := []struct {
		name string
		src  []chat.Source
		want int
	}{
		{"nil slice", nil, 0},
		{"empty slice", []chat.Source{}, 0},
		{"single source", []chat.Source{{File: "a.go", StartLine: 1, EndLine: 2, Content: "x", Score: 0.9, Keyword: false, Project: "proj"}}, 1},
		{"multiple sources", []chat.Source{
			{File: "a.go", StartLine: 1, EndLine: 2, Content: "x", Score: 0.9},
			{File: "b.go", StartLine: 3, EndLine: 4, Content: "y", Score: 0.8, Keyword: true, Project: "proj2"},
		}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := chatSourcesJSON(tt.src)
			if len(got) != tt.want {
				t.Fatalf("len = %d; want %d", len(got), tt.want)
			}
			for i, m := range got {
				if m["file"] != tt.src[i].File {
					t.Errorf("[%d] file = %v; want %s", i, m["file"], tt.src[i].File)
				}
				if m["score"] != tt.src[i].Score {
					t.Errorf("[%d] score = %v; want %f", i, m["score"], tt.src[i].Score)
				}
				if m["keyword"] != tt.src[i].Keyword {
					t.Errorf("[%d] keyword = %v; want %v", i, m["keyword"], tt.src[i].Keyword)
				}
				if m["project"] != tt.src[i].Project {
					t.Errorf("[%d] project = %v; want %s", i, m["project"], tt.src[i].Project)
				}
			}
		})
	}
}
