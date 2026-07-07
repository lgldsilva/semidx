package chat

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockClient returns a fixed response or error.
type mockClient struct {
	resp *Response
	err  error
}

func (m *mockClient) SendMessage(_ context.Context, req Request) (*Response, error) {
	return m.resp, m.err
}

func TestChainFirstProviderSucceeds(t *testing.T) {
	t.Parallel()

	first := &mockClient{
		resp: &Response{Content: "from first", Model: "model-a"},
	}
	second := &mockClient{
		resp: &Response{Content: "should not be reached", Model: "model-b"},
	}

	cc := NewChain(NamedClient{Name: "first", Client: first}, NamedClient{Name: "second", Client: second})
	resp, err := cc.SendMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
		Model:    "model-a",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if resp.Content != "from first" {
		t.Errorf("Content = %q, want %q", resp.Content, "from first")
	}
	if resp.Model != "model-a" {
		t.Errorf("Model = %q, want model-a", resp.Model)
	}
}

func TestChainFallsThroughOn429(t *testing.T) {
	t.Parallel()

	first := &mockClient{
		err: &HTTPError{StatusCode: http.StatusTooManyRequests, Body: "rate limited"},
	}
	second := &mockClient{
		resp: &Response{Content: "from fallback", Model: "model-b"},
	}

	cc := NewChain(NamedClient{Name: "first", Client: first}, NamedClient{Name: "second", Client: second})
	resp, err := cc.SendMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
		Model:    "requested-model",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if resp.Content != "from fallback" {
		t.Errorf("Content = %q, want %q", resp.Content, "from fallback")
	}
	// Model comes from the responding provider, not the request.
	if resp.Model != "model-b" {
		t.Errorf("Model = %q, want model-b", resp.Model)
	}
}

func TestChainFallsThroughOn5xx(t *testing.T) {
	t.Parallel()

	first := &mockClient{
		err: &HTTPError{StatusCode: http.StatusInternalServerError, Body: "internal error"},
	}
	second := &mockClient{
		resp: &Response{Content: "recovery", Model: "model-c"},
	}

	cc := NewChain(NamedClient{Name: "first", Client: first}, NamedClient{Name: "second", Client: second})
	resp, err := cc.SendMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if resp.Content != "recovery" {
		t.Errorf("Content = %q, want %q", resp.Content, "recovery")
	}
}

func TestChainFallsThroughOnTransportError(t *testing.T) {
	t.Parallel()

	first := &mockClient{
		err: errors.New("connection refused"),
	}
	second := &mockClient{
		resp: &Response{Content: "saved by fallback", Model: "model-d"},
	}

	cc := NewChain(NamedClient{Name: "first", Client: first}, NamedClient{Name: "second", Client: second})
	resp, err := cc.SendMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if resp.Content != "saved by fallback" {
		t.Errorf("Content = %q, want %q", resp.Content, "saved by fallback")
	}
}

func TestChainAllProvidersFail(t *testing.T) {
	t.Parallel()

	first := &mockClient{
		err: &HTTPError{StatusCode: http.StatusTooManyRequests, Body: "rate limit"},
	}
	second := &mockClient{
		err: errors.New("network error"),
	}

	cc := NewChain(NamedClient{Name: "first", Client: first}, NamedClient{Name: "second", Client: second})
	_, err := cc.SendMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
	msg := err.Error()
	if !strings.Contains(msg, "all chat providers failed") {
		t.Errorf("err = %q, want to contain 'all chat providers failed'", msg)
	}
	if !strings.Contains(msg, "Check your API keys") {
		t.Errorf("err = %q, want to contain API key hint", msg)
	}
	if !strings.Contains(msg, "rate limit") {
		t.Errorf("err = %q, want to contain first provider error details", msg)
	}
	if !strings.Contains(msg, "network error") {
		t.Errorf("err = %q, want to contain second provider error details", msg)
	}
}

func TestChainModelPreservedFromRespondingProvider(t *testing.T) {
	t.Parallel()

	first := &mockClient{
		err: &HTTPError{StatusCode: http.StatusServiceUnavailable, Body: "down"},
	}
	second := &mockClient{
		resp: &Response{Content: "answer", Model: "fallback-model"},
	}

	cc := NewChain(NamedClient{Name: "first", Client: first}, NamedClient{Name: "second", Client: second})
	resp, err := cc.SendMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "q"}},
		Model:    "requested-model",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	// Model should be from responding provider, not request.
	if resp.Model != "fallback-model" {
		t.Errorf("Model = %q, want fallback-model", resp.Model)
	}
}

func TestChainModelFallsBackToRequestModel(t *testing.T) {
	t.Parallel()

	first := &mockClient{
		resp: &Response{Content: "answer", Model: ""}, // model empty in response
	}

	cc := NewChain(NamedClient{Name: "first", Client: first})
	resp, err := cc.SendMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "q"}},
		Model:    "requested-model",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	// Model should fall back to request model when response model is empty.
	if resp.Model != "requested-model" {
		t.Errorf("Model = %q, want requested-model", resp.Model)
	}
}

func TestChainContextCancellation(t *testing.T) {
	t.Parallel()

	// Create a client that blocks forever.
	blocking := &blockingClient{waitCh: make(chan struct{})}
	fallback := &mockClient{
		resp: &Response{Content: "should not be called", Model: "m"},
	}

	cc := NewChain(NamedClient{Name: "blocking", Client: blocking}, NamedClient{Name: "fallback", Client: fallback})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancelled

	_, err := cc.SendMessage(ctx, Request{
		Messages: []Message{{Role: "user", Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestChainContextCancellationDuringBlockingProvider(t *testing.T) {
	t.Parallel()

	// First provider blocks; cancelling the context should propagate through.
	blocking := &blockingClient{waitCh: make(chan struct{})}
	fallback := &mockClient{
		resp: &Response{Content: "should not be called", Model: "m"},
	}

	cc := NewChain(NamedClient{Name: "blocking", Client: blocking}, NamedClient{Name: "fallback", Client: fallback})

	ctx, cancel := context.WithCancel(context.Background())

	// Start the chain in a goroutine — the first provider blocks.
	type result struct {
		resp *Response
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		resp, err := cc.SendMessage(ctx, Request{
			Messages: []Message{{Role: "user", Content: "x"}},
		})
		ch <- result{resp, err}
	}()

	// Give the goroutine time to enter the blocking client.
	time.Sleep(10 * time.Millisecond)
	cancel()

	r := <-ch
	if r.err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if !errors.Is(r.err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", r.err)
	}
}

func TestChainNoProviders(t *testing.T) {
	t.Parallel()

	cc := NewChain()
	_, err := cc.SendMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error with no providers")
	}
	if !strings.Contains(err.Error(), "no chat providers configured") {
		t.Errorf("err = %v, want 'no chat providers configured'", err)
	}
}

// blockingClient blocks forever (or until context is cancelled).
type blockingClient struct {
	waitCh chan struct{}
}

func (b *blockingClient) SendMessage(ctx context.Context, _ Request) (*Response, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.waitCh:
		return &Response{Content: "unblocked", Model: "m"}, nil
	}
}

func TestChainOnFallbackCalled(t *testing.T) {
	t.Parallel()

	first := &mockClient{
		err: &HTTPError{StatusCode: http.StatusTooManyRequests, Body: "exceeded quota"},
	}
	second := &mockClient{
		resp: &Response{Content: "fallback answer", Model: "model-fallback"},
	}

	var called bool
	var fallbackName string
	var fallbackErr error

	cc := NewChain(NamedClient{Name: "gemini", Client: first}, NamedClient{Name: "openrouter", Client: second})
	cc.OnFallback = func(name string, err error) {
		called = true
		fallbackName = name
		fallbackErr = err
	}

	resp, err := cc.SendMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
		Model:    "test-model",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if resp.Content != "fallback answer" {
		t.Errorf("Content = %q, want 'fallback answer'", resp.Content)
	}
	if !called {
		t.Fatal("OnFallback was not called")
	}
	if fallbackName != "gemini" {
		t.Errorf("fallbackName = %q, want 'gemini'", fallbackName)
	}
	if fallbackErr == nil {
		t.Fatal("fallbackErr should not be nil")
	}
	var httpErr *HTTPError
	if !errors.As(fallbackErr, &httpErr) {
		t.Fatalf("fallbackErr type = %T, want *HTTPError", fallbackErr)
	}
	if httpErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, want 429", httpErr.StatusCode)
	}
}

func TestChainOnFallbackNotCalledOnSuccess(t *testing.T) {
	t.Parallel()

	first := &mockClient{
		resp: &Response{Content: "success", Model: "m"},
	}

	var called bool
	cc := NewChain(NamedClient{Name: "ok", Client: first})
	cc.OnFallback = func(name string, err error) {
		called = true
	}

	_, err := cc.SendMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if called {
		t.Fatal("OnFallback should not be called when provider succeeds")
	}
}

func TestChainOnFallbackAllFail(t *testing.T) {
	t.Parallel()

	first := &mockClient{err: errors.New("first down")}
	second := &mockClient{err: errors.New("second down")}

	var calls []string
	cc := NewChain(NamedClient{Name: "a", Client: first}, NamedClient{Name: "b", Client: second})
	cc.OnFallback = func(name string, err error) {
		calls = append(calls, name)
	}

	_, err := cc.SendMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if len(calls) != 2 {
		t.Fatalf("OnFallback called %d times, want 2", len(calls))
	}
	if calls[0] != "a" || calls[1] != "b" {
		t.Errorf("calls = %v, want [a b]", calls)
	}
}

func TestChainDiagnosticsAfterAllFail(t *testing.T) {
	t.Parallel()

	first := &mockClient{
		err: &HTTPError{StatusCode: http.StatusUnauthorized, Body: "invalid key"},
	}
	second := &mockClient{
		err: errors.New("connection timeout"),
	}

	cc := NewChain(NamedClient{Name: "gemini", Client: first}, NamedClient{Name: "openrouter", Client: second})
	_, err := cc.SendMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}

	// Diagnostics returns nil after the field was made local (per-call).
	if diags := cc.Diagnostics(); len(diags) != 0 {
		t.Errorf("Diagnostics = %v, want empty (per-call errors are local)", diags)
	}

	// Error message still contains provider details.
	msg := err.Error()
	if !strings.Contains(msg, "invalid key") {
		t.Errorf("err = %q, want to contain 'invalid key'", msg)
	}
	if !strings.Contains(msg, "connection timeout") {
		t.Errorf("err = %q, want to contain 'connection timeout'", msg)
	}
}

func TestChainDiagnosticsEmptyAfterSuccess(t *testing.T) {
	t.Parallel()

	cc := NewChain(NamedClient{Name: "ok", Client: &mockClient{resp: &Response{Content: "ok", Model: "m"}}})
	_, err := cc.SendMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if diags := cc.Diagnostics(); len(diags) != 0 {
		t.Errorf("Diagnostics = %v, want empty", diags)
	}
}

// ---------------------------------------------------------------------------
// Streaming chain tests
// ---------------------------------------------------------------------------

// mockStreamClient implements both Client and StreamClient for testing.
type mockStreamClient struct {
	chunks []StreamChunk
	err    error
}

func (m *mockStreamClient) SendMessage(_ context.Context, req Request) (*Response, error) {
	// Collect all content from chunks into a single response.
	var b strings.Builder
	for _, c := range m.chunks {
		if c.Done {
			break
		}
		b.WriteString(c.Content)
	}
	model := ""
	for _, c := range m.chunks {
		if c.Model != "" {
			model = c.Model
			break
		}
	}
	return &Response{Content: b.String(), Model: model}, nil
}

func (m *mockStreamClient) StreamMessage(_ context.Context, req Request) (<-chan StreamChunk, error) {
	if m.err != nil {
		return nil, m.err
	}
	ch := make(chan StreamChunk, len(m.chunks)+1)
	for _, c := range m.chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

func TestChainStreamFirstProviderSucceeds(t *testing.T) {
	t.Parallel()

	first := &mockStreamClient{
		chunks: []StreamChunk{
			{Content: "Hello ", Model: "model-a"},
			{Content: "World"},
			{Done: true, Model: "model-a"},
		},
	}
	second := &mockStreamClient{
		chunks: []StreamChunk{
			{Content: "should not be reached"},
			{Done: true},
		},
	}

	cc := NewChain(NamedClient{Name: "first", Client: first}, NamedClient{Name: "second", Client: second})
	ch, err := cc.StreamMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamMessage: %v", err)
	}

	var got strings.Builder
	var gotDone bool
	for c := range ch {
		if c.Done {
			gotDone = true
			if c.Model != "" {
				t.Logf("model = %s", c.Model)
			}
			break
		}
		got.WriteString(c.Content)
	}

	if !gotDone {
		t.Fatal("expected Done chunk")
	}
	if got.String() != "Hello World" {
		t.Errorf("content = %q, want %q", got.String(), "Hello World")
	}
}

func TestChainStreamFallbackToNonStreaming(t *testing.T) {
	t.Parallel()

	// First provider does NOT implement StreamClient.
	first := &mockClient{
		resp: &Response{Content: "from non-streaming", Model: "model-a"},
	}
	second := &mockStreamClient{
		chunks: []StreamChunk{
			{Content: "should not be reached"},
			{Done: true},
		},
	}

	cc := NewChain(NamedClient{Name: "first", Client: first}, NamedClient{Name: "second", Client: second})
	ch, err := cc.StreamMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamMessage: %v", err)
	}

	var got strings.Builder
	var gotDone bool
	for c := range ch {
		if c.Done {
			gotDone = true
			break
		}
		got.WriteString(c.Content)
	}

	if !gotDone {
		t.Fatal("expected Done chunk")
	}
	if got.String() != "from non-streaming" {
		t.Errorf("content = %q, want %q", got.String(), "from non-streaming")
	}
}

func TestChainStreamFallsThroughOnStreamInitError(t *testing.T) {
	t.Parallel()

	first := &mockStreamClient{
		err: &HTTPError{StatusCode: http.StatusTooManyRequests, Body: "rate limited"},
	}
	second := &mockStreamClient{
		chunks: []StreamChunk{
			{Content: "from fallback", Model: "model-b"},
			{Done: true, Model: "model-b"},
		},
	}

	var called bool
	cc := NewChain(NamedClient{Name: "first", Client: first}, NamedClient{Name: "second", Client: second})
	cc.OnFallback = func(name string, err error) {
		called = true
	}

	ch, err := cc.StreamMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamMessage: %v", err)
	}

	var got strings.Builder
	var gotDone bool
	for c := range ch {
		if c.Done {
			gotDone = true
			break
		}
		got.WriteString(c.Content)
	}

	if !gotDone {
		t.Fatal("expected Done chunk")
	}
	if got.String() != "from fallback" {
		t.Errorf("content = %q, want %q", got.String(), "from fallback")
	}
	if !called {
		t.Error("OnFallback was not called")
	}
}

func TestChainStreamAllProvidersFail(t *testing.T) {
	t.Parallel()

	first := &mockStreamClient{
		err: &HTTPError{StatusCode: http.StatusTooManyRequests, Body: "rate limited"},
	}
	second := &mockStreamClient{
		err: errors.New("network error"),
	}

	cc := NewChain(NamedClient{Name: "first", Client: first}, NamedClient{Name: "second", Client: second})
	_, err := cc.StreamMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
	if !strings.Contains(err.Error(), "all streaming providers failed") {
		t.Errorf("err = %v, want 'all streaming providers failed'", err)
	}
}

func TestChainStreamContextCancellation(t *testing.T) {
	t.Parallel()

	blocking := &mockStreamClient{
		// A channel that never sends anything — blocks forever.
		chunks: nil,
	}

	cc := NewChain(NamedClient{Name: "blocking", Client: blocking})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancelled

	_, err := cc.StreamMessage(ctx, Request{
		Messages: []Message{{Role: "user", Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// Test fallback from streaming client that fails to non-streaming client.
func TestChainStreamStreamToNonStreamingFallback(t *testing.T) {
	t.Parallel()

	first := &mockStreamClient{
		err: errors.New("stream unavailable"),
	}
	second := &mockClient{
		resp: &Response{Content: "non-streaming fallback", Model: "model-b"},
	}

	var called bool
	cc := NewChain(NamedClient{Name: "a", Client: first}, NamedClient{Name: "b", Client: second})
	cc.OnFallback = func(name string, err error) {
		called = true
	}

	ch, err := cc.StreamMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamMessage: %v", err)
	}

	var got string
	var gotDone bool
	for c := range ch {
		if c.Done {
			gotDone = true
			break
		}
		got += c.Content
	}

	if !gotDone {
		t.Fatal("expected Done chunk")
	}
	if got != "non-streaming fallback" {
		t.Errorf("content = %q, want %q", got, "non-streaming fallback")
	}
	if !called {
		t.Error("OnFallback was not called")
	}
}

// Compile-time check: ChainClient implements StreamClient.
var _ StreamClient = (*ChainClient)(nil)

func TestChainSendMessageConcurrent(t *testing.T) {
	t.Parallel()

	slow := &mockClient{
		resp: &Response{Content: "slow", Model: "m"},
	}
	cc := NewChain(NamedClient{Name: "slow", Client: slow})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := cc.SendMessage(context.Background(), Request{
				Messages: []Message{{Role: "user", Content: "hi"}},
			})
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
}
