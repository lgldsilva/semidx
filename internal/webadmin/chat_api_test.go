package webadmin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/chat"
	"github.com/lgldsilva/semidx/internal/rag"
)

type fakeChat struct{}

func (fakeChat) Ask(_ context.Context, _ string, _ string, _ []chat.Message) (*rag.Answer, error) {
	return &rag.Answer{
		Content: "answer",
		Model:   "test-model",
		Sources: []rag.Source{{File: "a.go", StartLine: 1, EndLine: 2, Content: "x", Score: 0.9}},
	}, nil
}

func (fakeChat) StreamAsk(_ context.Context, _ string, _ string, _ []chat.Message) (<-chan chat.StreamChunk, []rag.Source, string, bool, error) {
	ch := make(chan chat.StreamChunk, 2)
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

type errChat struct{}

func (errChat) Ask(context.Context, string, string, []chat.Message) (*rag.Answer, error) {
	return nil, errors.New("upstream failed")
}
func (errChat) StreamAsk(context.Context, string, string, []chat.Message) (<-chan chat.StreamChunk, []rag.Source, string, bool, error) {
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
