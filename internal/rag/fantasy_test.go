package rag

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/chat"
)

// recordingSearch implements SearchService, capturing the last request.
type recordingSearch struct {
	req  SearchRequest
	resp *SearchResponse
	err  error
}

func (r *recordingSearch) Search(_ context.Context, req SearchRequest) (*SearchResponse, error) {
	r.req = req
	if r.err != nil {
		return nil, r.err
	}
	return r.resp, nil
}

// fakeRunner implements ChatRunner, capturing what the pipeline sends and
// replaying a scripted answer / stream.
type fakeRunner struct {
	answer *agent.Answer
	err    error
	deltas []string // streamed text deltas before the answer

	lastQuestion string
	lastMsgs     []fantasy.Message

	blockUntilCancel bool // Stream blocks until ctx is canceled, then errors
	streamStarted    chan struct{}
}

func (f *fakeRunner) Ask(_ context.Context, question string, history []fantasy.Message) (*agent.Answer, error) {
	f.lastQuestion, f.lastMsgs = question, history
	if f.err != nil {
		return nil, f.err
	}
	return f.answer, nil
}

func (f *fakeRunner) Stream(ctx context.Context, question string, history []fantasy.Message, cb agent.StreamCallbacks) (*agent.Answer, error) {
	f.lastQuestion, f.lastMsgs = question, history
	if f.streamStarted != nil {
		close(f.streamStarted)
	}
	if f.blockUntilCancel {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.err != nil {
		return nil, f.err
	}
	for _, d := range f.deltas {
		if cb.OnText != nil {
			cb.OnText(d)
		}
	}
	return f.answer, nil
}

func (f *fakeRunner) CompactHistory(_ context.Context, history []fantasy.Message) []fantasy.Message {
	return history
}

func (f *fakeRunner) Model() string { return "fake-model" }

func twoResults() *SearchResponse {
	return &SearchResponse{Results: []SearchResult{
		{FilePath: "internal/foo.go", Content: "func Foo() int { return 42 }", Score: 0.95, StartLine: 10, EndLine: 12},
		{FilePath: "internal/bar.go", Content: "func Bar() string { return \"hi\" }", Score: 0.85, StartLine: 5, EndLine: 8},
	}}
}

func TestFantasyPipelineAsk(t *testing.T) {
	t.Parallel()
	search := &recordingSearch{resp: twoResults()}
	runner := &fakeRunner{answer: &agent.Answer{Content: "Foo returns 42.", Model: "fake-model"}}
	p := NewFantasyPipeline(search, runner, PipelineConfig{TopK: 7})

	history := []chat.Message{{Role: "user", Content: "earlier q"}, {Role: "assistant", Content: "earlier a"}}
	ans, err := p.Ask(context.Background(), "What does Foo do?", "myproject", history)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if ans.Content != "Foo returns 42." || ans.Model != "fake-model" {
		t.Errorf("answer = %q model=%q", ans.Content, ans.Model)
	}
	if len(ans.Sources) != 2 || ans.Sources[0].File != "internal/foo.go" {
		t.Errorf("sources = %+v", ans.Sources)
	}
	if ans.Fallback || ans.Keyword {
		t.Errorf("flags = fallback=%v keyword=%v, want false", ans.Fallback, ans.Keyword)
	}
	if search.req.Project != "myproject" || search.req.Query != "What does Foo do?" || search.req.TopK != 7 {
		t.Errorf("search request = %+v", search.req)
	}
	// The runner gets: [system(context)] + compacted history; the question rides
	// the prompt argument, never the history.
	if runner.lastQuestion != "What does Foo do?" {
		t.Errorf("question = %q", runner.lastQuestion)
	}
	if len(runner.lastMsgs) != 3 {
		t.Fatalf("msgs = %d, want system + 2 history", len(runner.lastMsgs))
	}
	sys := runner.lastMsgs[0]
	if sys.Role != fantasy.MessageRoleSystem {
		t.Fatalf("first message role = %v, want system", sys.Role)
	}
	sysText := fantasyText(t, sys)
	for _, want := range []string{"internal/foo.go", "func Foo() int", "lines 10-12", "helpful AI assistant"} {
		if !strings.Contains(sysText, want) {
			t.Errorf("system message misses %q:\n%s", want, sysText)
		}
	}
	if runner.lastMsgs[1].Role != fantasy.MessageRoleUser || runner.lastMsgs[2].Role != fantasy.MessageRoleAssistant {
		t.Errorf("history roles = %v/%v", runner.lastMsgs[1].Role, runner.lastMsgs[2].Role)
	}
}

// fantasyText concatenates the text parts of a fantasy message.
func fantasyText(t *testing.T, m fantasy.Message) string {
	t.Helper()
	var b strings.Builder
	for _, p := range m.Content {
		if tp, ok := fantasy.AsMessagePart[fantasy.TextPart](p); ok {
			b.WriteString(tp.Text)
		}
	}
	return b.String()
}

func TestFantasyPipelineAskFallbackFlags(t *testing.T) {
	t.Parallel()
	resp := twoResults()
	resp.Fallback, resp.Keyword = true, true
	p := NewFantasyPipeline(&recordingSearch{resp: resp},
		&fakeRunner{answer: &agent.Answer{Content: "a", Model: "fake-model"}}, PipelineConfig{})
	ans, err := p.Ask(context.Background(), "q", "proj", nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if !ans.Fallback || !ans.Keyword {
		t.Errorf("flags = fallback=%v keyword=%v, want true", ans.Fallback, ans.Keyword)
	}
}

func TestFantasyPipelineAskSearchError(t *testing.T) {
	t.Parallel()
	p := NewFantasyPipeline(&recordingSearch{err: errors.New("db down")}, &fakeRunner{}, PipelineConfig{})
	_, err := p.Ask(context.Background(), "q", "proj", nil)
	if err == nil || !strings.Contains(err.Error(), "search failed") {
		t.Fatalf("err = %v, want search failed", err)
	}
}

func TestFantasyPipelineAskChatError(t *testing.T) {
	t.Parallel()
	p := NewFantasyPipeline(&recordingSearch{resp: twoResults()},
		&fakeRunner{err: errors.New("provider 500")}, PipelineConfig{})
	_, err := p.Ask(context.Background(), "q", "proj", nil)
	if err == nil || !strings.Contains(err.Error(), "chat failed") {
		t.Fatalf("err = %v, want chat failed", err)
	}
}

func TestNewFantasyPipelineTopKDefault(t *testing.T) {
	t.Parallel()
	search := &recordingSearch{resp: &SearchResponse{}}
	p := NewFantasyPipeline(search, &fakeRunner{answer: &agent.Answer{}}, PipelineConfig{})
	if _, err := p.Ask(context.Background(), "q", "proj", nil); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if search.req.TopK != 5 {
		t.Errorf("TopK = %d, want default 5", search.req.TopK)
	}
}

// drain collects chunks until the channel closes, guarding against leaks.
func drain(t *testing.T, ch <-chan chat.StreamChunk) []chat.StreamChunk {
	t.Helper()
	var got []chat.StreamChunk
	deadline := time.After(10 * time.Second)
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, c)
		case <-deadline:
			t.Fatal("stream channel did not close — goroutine leak")
		}
	}
}

func TestFantasyPipelineStreamAsk(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{
		answer: &agent.Answer{Content: "the answer", Model: "fake-model"},
		deltas: []string{"the ", "answer"},
	}
	p := NewFantasyPipeline(&recordingSearch{resp: twoResults()}, runner, PipelineConfig{})

	ch, sources, model, fallback, err := p.StreamAsk(context.Background(), "q", "proj", nil)
	if err != nil {
		t.Fatalf("StreamAsk: %v", err)
	}
	if model != "fake-model" || fallback {
		t.Errorf("model=%q fallback=%v", model, fallback)
	}
	// Sources are known up front (search is synchronous).
	if len(sources) != 2 {
		t.Fatalf("sources = %d, want 2", len(sources))
	}
	chunks := drain(t, ch)
	var text strings.Builder
	var done *chat.StreamChunk
	for i, c := range chunks {
		if c.Done {
			done = &chunks[i]
			continue
		}
		text.WriteString(c.Content)
	}
	if text.String() != "the answer" {
		t.Errorf("streamed text = %q", text.String())
	}
	if done == nil || done.Model != "fake-model" || done.Err != "" {
		t.Errorf("terminal chunk = %+v", done)
	}
}

func TestFantasyPipelineStreamSearchError(t *testing.T) {
	t.Parallel()
	p := NewFantasyPipeline(&recordingSearch{err: errors.New("db down")}, &fakeRunner{}, PipelineConfig{})
	_, _, _, _, err := p.StreamAsk(context.Background(), "q", "proj", nil)
	if err == nil || !strings.Contains(err.Error(), "search failed") {
		t.Fatalf("err = %v, want search failed", err)
	}
}

// TestFantasyPipelineStreamErrorIsGeneric: a mid-stream runner failure must
// surface only the generic backend message — provider errors can carry URLs
// or keys and never reach the client.
func TestFantasyPipelineStreamErrorIsGeneric(t *testing.T) {
	t.Parallel()
	provErr := errors.New("POST https://llm.example/key=sk-secret: 500")
	p := NewFantasyPipeline(&recordingSearch{resp: twoResults()},
		&fakeRunner{err: provErr}, PipelineConfig{})
	ch, _, _, _, err := p.StreamAsk(context.Background(), "q", "proj", nil)
	if err != nil {
		t.Fatalf("StreamAsk: %v", err)
	}
	chunks := drain(t, ch)
	if len(chunks) == 0 {
		t.Fatal("expected a terminal chunk")
	}
	last := chunks[len(chunks)-1]
	if !last.Done || last.Err != fantasyBackendErrMsg {
		t.Errorf("terminal chunk = %+v, want Done with the generic message", last)
	}
	if strings.Contains(last.Err, "sk-secret") {
		t.Errorf("provider error leaked: %q", last.Err)
	}
}

// TestFantasyPipelineStreamCancel is the cancellation regression: canceling the
// request context must close the channel promptly and must NOT surface a
// backend-error chunk — the client went away, nothing failed.
func TestFantasyPipelineStreamCancel(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{blockUntilCancel: true, streamStarted: make(chan struct{})}
	p := NewFantasyPipeline(&recordingSearch{resp: twoResults()}, runner, PipelineConfig{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _, _, _, err := p.StreamAsk(ctx, "q", "proj", nil)
	if err != nil {
		t.Fatalf("StreamAsk: %v", err)
	}
	select {
	case <-runner.streamStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("stream never reached the runner")
	}
	cancel()
	for _, c := range drain(t, ch) {
		if c.Err != "" {
			t.Errorf("cancellation must not surface a backend error, got %q", c.Err)
		}
	}
}
