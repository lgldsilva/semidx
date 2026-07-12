package rag

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/chat"
)

// fakeSearchService implements SearchService with configurable results.
type fakeSearchService struct {
	results []SearchResult
	err     error
}

func (f *fakeSearchService) Search(_ context.Context, _ SearchRequest) (*SearchResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &SearchResponse{Results: f.results}, nil
}

// fakeChatClient implements chat.Client with configurable responses.
// It captures the last request for test assertions.
type fakeChatClient struct {
	content     string
	model       string
	err         error
	lastRequest chat.Request // captured for assertions
}

func (f *fakeChatClient) SendMessage(_ context.Context, req chat.Request) (*chat.Response, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.lastRequest = req

	return &chat.Response{
		Content: f.content,
		Model:   f.model,
	}, nil
}

func TestPipelineAskSuccess(t *testing.T) {
	t.Parallel()

	search := &fakeSearchService{
		results: []SearchResult{
			{
				FilePath:  "internal/foo.go",
				Content:   "func Foo() { return 42 }",
				Score:     0.95,
				StartLine: 10,
				EndLine:   12,
			},
			{
				FilePath:  "internal/bar.go",
				Content:   "func Bar() string { return \"hello\" }",
				Score:     0.85,
				StartLine: 5,
				EndLine:   8,
			},
		},
	}
	chatClient := &fakeChatClient{
		content: "Foo returns 42.",
		model:   "gemini-2.5-flash",
	}

	pipe := NewPipeline(search, chatClient, PipelineConfig{
		TopK:      5,
		MaxTokens: 4096,
		Model:     "gemini-2.5-flash",
	})

	answer, err := pipe.Ask(context.Background(), "What does Foo do?", "myproject", nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	if answer.Content != "Foo returns 42." {
		t.Errorf("Content = %q, want %q", answer.Content, "Foo returns 42.")
	}
	if answer.Model != "gemini-2.5-flash" {
		t.Errorf("Model = %q, want gemini-2.5-flash", answer.Model)
	}
	if len(answer.Sources) != 2 {
		t.Fatalf("got %d sources, want 2", len(answer.Sources))
	}
	if answer.Sources[0].File != "internal/foo.go" {
		t.Errorf("Source[0].File = %q, want internal/foo.go", answer.Sources[0].File)
	}
	if answer.Sources[0].Score != 0.95 {
		t.Errorf("Source[0].Score = %v, want 0.95", answer.Sources[0].Score)
	}

	// N5: Verify messages shape in the captured request.
	msgs := chatClient.lastRequest.Messages
	if len(msgs) < 2 {
		t.Fatalf("got %d messages, want at least 2 (system + user)", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Errorf("msgs[0].Role = %q, want system", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, systemPrompt) {
		t.Errorf("system message should contain system prompt")
	}
	if !strings.Contains(msgs[0].Content, "func Foo()") {
		t.Errorf("system message should contain context content")
	}
	if !strings.Contains(msgs[0].Content, "func Bar()") {
		t.Errorf("system message should contain all context content")
	}
	last := msgs[len(msgs)-1]
	if last.Role != "user" || last.Content != "What does Foo do?" {
		t.Errorf("last message = %+v, want user question", last)
	}
}

func TestPipelineAskNoResults(t *testing.T) {
	t.Parallel()

	search := &fakeSearchService{results: nil}
	chatClient := &fakeChatClient{
		content: "I don't have enough context to answer.",
		model:   "gemini-2.5-flash",
	}

	pipe := NewPipeline(search, chatClient, PipelineConfig{TopK: 5, MaxTokens: 4096})

	answer, err := pipe.Ask(context.Background(), "What does Foo do?", "myproject", nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	if len(answer.Sources) != 0 {
		t.Errorf("got %d sources, want 0", len(answer.Sources))
	}
	if answer.Content == "" {
		t.Error("Content should not be empty even with no results")
	}
}

func TestPipelineAskSearchError(t *testing.T) {
	t.Parallel()

	search := &fakeSearchService{err: errors.New("search down")}
	chatClient := &fakeChatClient{}

	pipe := NewPipeline(search, chatClient, PipelineConfig{TopK: 5, MaxTokens: 4096})

	_, err := pipe.Ask(context.Background(), "question", "proj", nil)
	if err == nil {
		t.Fatal("expected error from search failure")
	}
	if !strings.Contains(err.Error(), "search failed") {
		t.Errorf("err = %v, want 'search failed' prefix", err)
	}
	if !strings.Contains(err.Error(), "search down") {
		t.Errorf("err = %v, want underlying search error", err)
	}
}

func TestPipelineAskChatError(t *testing.T) {
	t.Parallel()

	search := &fakeSearchService{
		results: []SearchResult{
			{FilePath: "a.go", Content: "code", Score: 0.5, StartLine: 1, EndLine: 1},
		},
	}
	chatClient := &fakeChatClient{err: errors.New("LLM unavailable")}

	pipe := NewPipeline(search, chatClient, PipelineConfig{TopK: 5, MaxTokens: 4096})

	_, err := pipe.Ask(context.Background(), "question", "proj", nil)
	if err == nil {
		t.Fatal("expected error from chat failure")
	}
	if !strings.Contains(err.Error(), "chat failed") {
		t.Errorf("err = %v, want 'chat failed' prefix", err)
	}
	if !strings.Contains(err.Error(), "LLM unavailable") {
		t.Errorf("err = %v, want underlying chat error", err)
	}
}

func TestPipelineContextAssembly(t *testing.T) {
	t.Parallel()

	search := &fakeSearchService{
		results: []SearchResult{
			{
				FilePath:  "internal/foo.go",
				Content:   "func Foo() int { return 42 }",
				Score:     0.95,
				StartLine: 10,
				EndLine:   12,
			},
		},
	}
	chatClient := &fakeChatClient{
		content: "answer",
		model:   "gpt-4",
	}

	pipe := NewPipeline(search, chatClient, PipelineConfig{TopK: 5, MaxTokens: 4096})

	answer, err := pipe.Ask(context.Background(), "What does Foo do?", "proj", nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	if len(answer.Sources) != 1 {
		t.Fatalf("got %d sources, want 1", len(answer.Sources))
	}

	s := answer.Sources[0]
	if s.File != "internal/foo.go" {
		t.Errorf("File = %q, want internal/foo.go", s.File)
	}
	if s.StartLine != 10 {
		t.Errorf("StartLine = %d, want 10", s.StartLine)
	}
	if s.EndLine != 12 {
		t.Errorf("EndLine = %d, want 12", s.EndLine)
	}
	if s.Score != 0.95 {
		t.Errorf("Score = %v, want 0.95", s.Score)
	}

	// Verify model is passed through.
	if answer.Model != "gpt-4" {
		t.Errorf("Model = %q, want gpt-4", answer.Model)
	}
}

func TestPipelineAskWithHistory(t *testing.T) {
	t.Parallel()

	search := &fakeSearchService{
		results: []SearchResult{
			{FilePath: "a.go", Content: "code", Score: 0.9, StartLine: 1, EndLine: 1},
		},
	}
	chatClient := &fakeChatClient{
		content: "some answer",
		model:   "gpt-4",
	}

	pipe := NewPipeline(search, chatClient, PipelineConfig{TopK: 5, MaxTokens: 4096})

	history := []chat.Message{
		{Role: "user", Content: "previous question"},
		{Role: "assistant", Content: "previous answer"},
	}

	answer, err := pipe.Ask(context.Background(), "follow-up", "proj", history)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if answer.Content != "some answer" {
		t.Errorf("Content = %q", answer.Content)
	}

	// Verify history was passed through.
	msgs := chatClient.lastRequest.Messages
	if len(msgs) != 4 {
		t.Fatalf("got %d messages, want 4 (system + 2 history + user)", len(msgs))
	}
	if msgs[1].Content != "previous question" {
		t.Errorf("msgs[1] = %q, want previous question", msgs[1].Content)
	}
	if msgs[2].Content != "previous answer" {
		t.Errorf("msgs[2] = %q, want previous answer", msgs[2].Content)
	}
	if msgs[3].Content != "follow-up" {
		t.Errorf("msgs[3] = %q, want follow-up", msgs[3].Content)
	}
}

func TestPipelineAskPopulatesSearchRequestFields(t *testing.T) {
	t.Parallel()

	var capturedReq SearchRequest
	search := &fakeSearchService{
		results: []SearchResult{
			{FilePath: "a.go", Content: "code", Score: 0.9, StartLine: 1, EndLine: 1},
		},
	}
	// Wrap the fake to capture the request.
	wrapped := &searchCaptureWrapper{inner: search, captured: &capturedReq}
	chatClient := &fakeChatClient{content: "ok", model: "m"}

	pipe := NewPipeline(wrapped, chatClient, PipelineConfig{
		TopK:      5,
		MaxTokens: 4096,
		Model:     "gemini-2.5-flash",
		Identity:  "my-repo-id",
		Worktree:  "/home/user/repo",
	})

	_, err := pipe.Ask(context.Background(), "question", "myproject", nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	if capturedReq.Project != "myproject" {
		t.Errorf("Project = %q, want myproject", capturedReq.Project)
	}
	if capturedReq.Query != "question" {
		t.Errorf("Query = %q, want question", capturedReq.Query)
	}
	if capturedReq.TopK != 5 {
		t.Errorf("TopK = %d, want 5", capturedReq.TopK)
	}
	if capturedReq.Identity != "my-repo-id" {
		t.Errorf("Identity = %q, want my-repo-id", capturedReq.Identity)
	}
	if capturedReq.Worktree != "/home/user/repo" {
		t.Errorf("Worktree = %q, want /home/user/repo", capturedReq.Worktree)
	}
}

// searchCaptureWrapper wraps a fakeSearchService to capture the request.
type searchCaptureWrapper struct {
	inner    *fakeSearchService
	captured *SearchRequest
}

func (w *searchCaptureWrapper) Search(_ context.Context, req SearchRequest) (*SearchResponse, error) {
	*w.captured = req
	return w.inner.Search(context.Background(), req)
}

func TestPipelineDefaultConfig(t *testing.T) {
	t.Parallel()

	search := &fakeSearchService{results: nil}
	chatClient := &fakeChatClient{content: "ok", model: "m"}

	pipe := NewPipeline(search, chatClient, PipelineConfig{})
	if pipe.config.TopK != 5 {
		t.Errorf("TopK default = %d, want 5", pipe.config.TopK)
	}
	if pipe.config.MaxTokens != 4096 {
		t.Errorf("MaxTokens default = %d, want 4096", pipe.config.MaxTokens)
	}
	// Temperature should not be defaulted — zero means deterministic mode.
	if pipe.config.Temperature != 0 {
		t.Errorf("Temperature should be 0 (not set), got %v", pipe.config.Temperature)
	}
}

type streamChatClient struct {
	fakeChatClient
	chunks    <-chan chat.StreamChunk
	streamErr error
}

func (s *streamChatClient) StreamMessage(context.Context, chat.Request) (<-chan chat.StreamChunk, error) {
	if s.streamErr != nil {
		return nil, s.streamErr
	}
	return s.chunks, nil
}

func TestPipelineStreamAskStreaming(t *testing.T) {
	t.Parallel()
	search := &fakeSearchService{
		results: []SearchResult{{FilePath: "a.go", Content: "code", Score: 0.9, StartLine: 1, EndLine: 1}},
	}
	ch := make(chan chat.StreamChunk, 2)
	ch <- chat.StreamChunk{Content: "hi"}
	ch <- chat.StreamChunk{Done: true}
	chatClient := &streamChatClient{
		fakeChatClient: fakeChatClient{model: "stream-model"},
		chunks:         ch,
	}
	pipe := NewPipeline(search, chatClient, PipelineConfig{TopK: 5, MaxTokens: 4096, Model: "stream-model"})
	out, sources, model, _, err := pipe.StreamAsk(context.Background(), "q", "proj", nil)
	if err != nil {
		t.Fatal(err)
	}
	if model != "stream-model" || len(sources) != 1 {
		t.Fatalf("model=%q sources=%d", model, len(sources))
	}
	chunk := <-out
	if chunk.Content != "hi" {
		t.Fatalf("chunk=%+v", chunk)
	}
}

func TestPipelineStreamAskFallback(t *testing.T) {
	t.Parallel()
	search := &fakeSearchService{
		results: []SearchResult{{FilePath: "a.go", Content: "code", Score: 0.9, StartLine: 1, EndLine: 1}},
	}
	chatClient := &fakeChatClient{content: "fallback answer", model: "m"}
	pipe := NewPipeline(search, chatClient, PipelineConfig{TopK: 5, MaxTokens: 4096, Model: "m"})
	out, _, model, _, err := pipe.StreamAsk(context.Background(), "q", "proj", nil)
	if err != nil {
		t.Fatal(err)
	}
	chunk := <-out
	if chunk.Content != "fallback answer" || model != "m" {
		t.Fatalf("chunk=%+v model=%q", chunk, model)
	}
}

func TestAssemblePrompt(t *testing.T) {
	t.Parallel()

	msgs := assemblePrompt("what is this?", "--- context ---", nil)
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2 (system + user)", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Errorf("msgs[0].Role = %q, want system", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, systemPrompt) {
		t.Errorf("system message should contain system prompt")
	}
	if !strings.Contains(msgs[0].Content, "--- context ---") {
		t.Errorf("system message should contain context")
	}
	if msgs[1].Role != "user" || msgs[1].Content != "what is this?" {
		t.Errorf("msgs[1] = %+v, want user msg", msgs[1])
	}
}

func TestAssemblePromptNoContext(t *testing.T) {
	t.Parallel()

	msgs := assemblePrompt("hello?", "", nil)
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2 (system + user)", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Errorf("msgs[0].Role = %q", msgs[0].Role)
	}
	if msgs[1].Role != "user" || msgs[1].Content != "hello?" {
		t.Errorf("msgs[1] = %+v", msgs[1])
	}
}

func TestAssemblePromptWithHistory(t *testing.T) {
	t.Parallel()

	history := []chat.Message{
		{Role: "user", Content: "previous question"},
		{Role: "assistant", Content: "previous answer"},
	}
	msgs := assemblePrompt("follow-up", "some context", history)

	// Single system message (instructions + context), then 2 history, then user.
	if len(msgs) != 4 {
		t.Fatalf("got %d messages, want 4 (system + 2 history + user)", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Errorf("msgs[0].Role = %q, want system", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, "some context") {
		t.Errorf("msgs[0] should contain context")
	}
	if msgs[1].Content != "previous question" {
		t.Errorf("msgs[1] = %q, want previous question", msgs[1].Content)
	}
	if msgs[2].Content != "previous answer" {
		t.Errorf("msgs[2] = %q, want previous answer", msgs[2].Content)
	}
	if msgs[3].Content != "follow-up" {
		t.Errorf("msgs[3] = %q, want follow-up", msgs[3].Content)
	}
}

func TestAssembleContextEmptySources(t *testing.T) {
	t.Parallel()

	got := assembleContext(nil, 8000)
	if got != "" {
		t.Errorf("assembleContext(nil, 8000) = %q, want empty", got)
	}

	got = assembleContext([]Source{}, 8000)
	if got != "" {
		t.Errorf("assembleContext([], 8000) = %q, want empty", got)
	}
}

func TestAssembleContextZeroBudget(t *testing.T) {
	t.Parallel()

	sources := []Source{
		{File: "a.go", Content: "some code", Score: 0.9, StartLine: 1, EndLine: 1},
	}

	got := assembleContext(sources, 0)
	if got != "" {
		t.Errorf("assembleContext with 0 budget = %q, want empty", got)
	}

	got = assembleContext(sources, -1)
	if got != "" {
		t.Errorf("assembleContext with negative budget = %q, want empty", got)
	}
}

func TestAssembleContextAllFit(t *testing.T) {
	t.Parallel()

	sources := []Source{
		{File: "a.go", Content: "short", Score: 0.9, StartLine: 1, EndLine: 1},
		{File: "b.go", Content: "tiny", Score: 0.8, StartLine: 2, EndLine: 2},
	}

	got := assembleContext(sources, 8000)
	if !strings.Contains(got, "a.go") {
		t.Errorf("context missing a.go")
	}
	if !strings.Contains(got, "b.go") {
		t.Errorf("context missing b.go")
	}
	if !strings.Contains(got, "short") {
		t.Errorf("context missing content")
	}
}

func TestAssembleContextTruncated(t *testing.T) {
	t.Parallel()

	// A single large source that exceeds the budget.
	longContent := strings.Repeat("hello world ", 500) // ~6000 chars
	sources := []Source{
		{File: "big.go", Content: longContent, Score: 0.9, StartLine: 1, EndLine: 100},
	}

	// Budget of 200 tokens = 800 chars - not enough for the full source.
	got := assembleContext(sources, 200)
	if got == "" {
		t.Fatal("context should not be empty with small budget")
	}
	if !strings.HasSuffix(strings.TrimSpace(got), "---") {
		t.Errorf("context should end with closing marker '---', got: %q", got)
	}
	if !strings.Contains(got, "hello world") {
		t.Errorf("context should contain content prefix")
	}
}

func TestAssembleContextDropsLowScoreWhenOversized(t *testing.T) {
	t.Parallel()

	// Multiple sources. Low budget means only the first (highest score) fits.
	sources := []Source{
		{File: "high.go", Content: "high score content", Score: 0.95, StartLine: 1, EndLine: 1},
		{File: "low.go", Content: "low score content", Score: 0.3, StartLine: 2, EndLine: 2},
	}

	// Very tight budget: ~20 tokens = 80 chars, enough for only one block.
	got := assembleContext(sources, 20)

	// The first/higher-score source should be present.
	if !strings.Contains(got, "high.go") || !strings.Contains(got, "high score content") {
		t.Errorf("highest score source should be included: %q", got)
	}
	// The second/lower-score source should be dropped.
	if strings.Contains(got, "low.go") {
		t.Errorf("low-score source should not be included: %q", got)
	}
}

func TestFormatSourceBlock(t *testing.T) {
	t.Parallel()

	s := Source{File: "test.go", StartLine: 5, EndLine: 8, Content: "line1\nline2", Score: 0.9}
	block := formatSourceBlock(s)

	if !strings.Contains(block, "test.go") {
		t.Errorf("block missing file path")
	}
	if !strings.Contains(block, "lines 5-8") {
		t.Errorf("block missing line range")
	}
	if !strings.Contains(block, "line1") {
		t.Errorf("block missing content")
	}
	if !strings.HasSuffix(block, "\n") {
		t.Errorf("block should end with newline")
	}
}
