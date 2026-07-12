package webchat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/chat"
)

// failWriter is an http.ResponseWriter whose Write always fails, to exercise
// template/encoder error branches.
type failWriter struct{ header http.Header }

func (f *failWriter) Header() http.Header {
	if f.header == nil {
		f.header = http.Header{}
	}
	return f.header
}
func (f *failWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }
func (f *failWriter) WriteHeader(int)           {}

func TestHandleChatPage_TemplateError(t *testing.T) {
	srv, err := New(&mockPipeline{}, "proj", ":0")
	if err != nil {
		t.Fatal(err)
	}
	// A failing writer makes ExecuteTemplate error out; handleChatPage must not
	// panic and takes the error branch (http.Error).
	srv.handleChatPage(&failWriter{}, httptest.NewRequest(http.MethodGet, "/chat", nil))
}

func TestResolveProject_Branches(t *testing.T) {
	// req empty + server default empty -> required error.
	s := &Server{}
	if _, err := s.resolveProject(""); err == nil {
		t.Fatal("expected 'project is required' when no default and none given")
	}
	// req empty + server default set -> returns default.
	s2 := &Server{project: "def"}
	if got, err := s2.resolveProject(""); err != nil || got != "def" {
		t.Fatalf("resolveProject(empty) = %q, %v; want def, nil", got, err)
	}
	// req matches default -> allowed.
	if got, err := s2.resolveProject("def"); err != nil || got != "def" {
		t.Fatalf("resolveProject(def) = %q, %v", got, err)
	}
	// req differs from default -> not allowed.
	if _, err := s2.resolveProject("other"); !errors.Is(err, errProjectNotAllowed) {
		t.Fatalf("resolveProject(other) err = %v, want errProjectNotAllowed", err)
	}
	// no default set -> any project accepted.
	s3 := &Server{}
	if got, err := s3.resolveProject("anything"); err != nil || got != "anything" {
		t.Fatalf("resolveProject(anything) = %q, %v", got, err)
	}
}

func TestResolveModel_AllBranches(t *testing.T) {
	s := &Server{}
	if got := s.resolveModel("cur", "chunk", "def"); got != "chunk" {
		t.Errorf("chunk model should win: %q", got)
	}
	if got := s.resolveModel("cur", "", "def"); got != "cur" {
		t.Errorf("current should win when no chunk model: %q", got)
	}
	if got := s.resolveModel("", "", "def"); got != "def" {
		t.Errorf("default should be the fallback: %q", got)
	}
}

func TestStreamChatChunks_ContextCancelled(t *testing.T) {
	srv, err := New(&mockPipeline{}, "test", ":0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already done before the loop reads
	rr := httptest.NewRecorder()
	ch := make(chan chat.StreamChunk) // never sends; ctx.Done wins
	model := srv.streamChatChunks(ctx, rr, rr, ch, "fallback-model")
	// On an immediately-cancelled context the loop returns without emitting a
	// done event; the resolved model stays empty.
	if model != "" {
		t.Errorf("cancelled stream resolved model = %q, want empty", model)
	}
}

func TestChatStream_ProjectOverrideRejected(t *testing.T) {
	srv, err := New(&mockPipeline{}, "my-project", ":0")
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	body := `{"question":"hi","project":"other"}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat/stream", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleChatStream(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("stream project override = %d, want 403", rr.Code)
	}
}

func TestIsLoopbackListen_NoPortAndGarbage(t *testing.T) {
	// bare host without a port: SplitHostPort errors, host falls back to addr.
	if !isLoopbackListen("localhost") {
		t.Error("bare 'localhost' should be treated as loopback")
	}
	if !isLoopbackListen("127.0.0.1") {
		t.Error("bare '127.0.0.1' should be loopback")
	}
	if isLoopbackListen("not-an-address") {
		t.Error("garbage host is not loopback")
	}
}

func TestListenAndServe_RefusesPublicBind(t *testing.T) {
	srv, err := New(&mockPipeline{}, "test", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	// No SEMIDX_CHATRAG_ALLOW_PUBLIC: ListenAndServe must return the bind-safety
	// error before ever listening.
	if err := srv.ListenAndServe(); err == nil {
		t.Fatal("ListenAndServe on a public addr without override must error")
	}
}

// nonFlusher wraps a recorder but deliberately does NOT implement http.Flusher,
// so handleChatStream takes its non-streaming fallback to handleChat.
type nonFlusher struct{ rr *httptest.ResponseRecorder }

func (n *nonFlusher) Header() http.Header         { return n.rr.Header() }
func (n *nonFlusher) Write(b []byte) (int, error) { return n.rr.Write(b) }
func (n *nonFlusher) WriteHeader(c int)           { n.rr.WriteHeader(c) }

func TestHandleChatStream_NonFlusherFallsBack(t *testing.T) {
	srv, err := New(&mockPipeline{answer: nil}, "test", ":0")
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	w := &nonFlusher{rr: rr}
	body := `{"question":"hi","project":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat/stream", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// w is not an http.Flusher, so handleChatStream delegates to handleChat.
	srv.handleChatStream(w, req)
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("fallback should use JSON handler, got Content-Type %q", ct)
	}
}

func TestStreamChatChunks_MidStreamDoneAndEmptyChunk(t *testing.T) {
	srv, err := New(&mockPipeline{}, "test", ":0")
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan chat.StreamChunk, 3)
	ch <- chat.StreamChunk{Content: ""}                // empty content: skipped
	ch <- chat.StreamChunk{Content: "hi", Model: "m1"} // sets resolvedModel
	ch <- chat.StreamChunk{Done: true, Model: "m2"}    // mid-stream done branch
	close(ch)
	rr := httptest.NewRecorder()
	model := srv.streamChatChunks(context.Background(), rr, rr, ch, "def")
	if model != "m2" {
		t.Errorf("mid-stream done model = %q, want m2", model)
	}
	if !strings.Contains(rr.Body.String(), "\"done\"") {
		t.Error("expected a done SSE event")
	}
}

func TestNew_NilPipelineStillBuilds(t *testing.T) {
	// New parses templates and never fails for a valid embedded FS; the nil
	// pipeline is a valid (page-only) configuration.
	if _, err := New(nil, "", "127.0.0.1:0"); err != nil {
		t.Fatalf("New: %v", err)
	}
}
