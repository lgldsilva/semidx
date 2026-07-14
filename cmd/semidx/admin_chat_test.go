package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/chat"
	"github.com/lgldsilva/semidx/internal/config"
	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/rag"
	"github.com/lgldsilva/semidx/internal/webadmin"
)

// stubEmbedder is a no-op embed.Embedder for wiring tests (no network).
type stubEmbedder struct{}

func (stubEmbedder) ModelInfo(context.Context, string) (*embed.ModelInfo, error) {
	return &embed.ModelInfo{Name: "m", Dims: 3}, nil
}
func (stubEmbedder) Embed(context.Context, string, ...string) ([][]float32, error) {
	return [][]float32{{0.1, 0.2, 0.3}}, nil
}
func (stubEmbedder) EmbedSingle(context.Context, string, string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}
func (stubEmbedder) ListModels(context.Context) ([]string, error) { return []string{"m"}, nil }

// TestBuildAdminChatPipeline_anyProvider is the audit regression (ALTA #3): the
// admin chat must enable for ANY provider ResolveChatLLM supports — not only
// Gemini/OpenRouter.
func TestBuildAdminChatPipeline_anyProvider(t *testing.T) {
	// openai-compatible (e.g. OpenCode Zen), with NO Gemini/OpenRouter keys.
	d := &deps{
		emb: stubEmbedder{},
		cfg: &config.Config{
			ChatProvider: "openai-compatible",
			ChatModel:    "deepseek-v4-flash-free",
			ChatAPIKey:   "k",
			ChatBaseURL:  "https://opencode.ai/zen/v1",
		},
	}
	p := d.buildAdminChatPipeline()
	if p == nil {
		t.Fatal("openai-compatible provider must enable the admin chat (gate removed)")
	}
	if _, ok := p.(*adminChat); !ok {
		t.Errorf("want the per-request adminChat factory, got %T", p)
	}

	// No chat provider at all → nil (chat unavailable).
	if got := (&deps{emb: stubEmbedder{}, cfg: &config.Config{}}).buildAdminChatPipeline(); got != nil {
		t.Errorf("no provider should yield nil chat, got %T", got)
	}

	// Gemini key (auto-detected by ResolveChatLLM) → agent chat.
	dGem := &deps{emb: stubEmbedder{}, cfg: &config.Config{GeminiAPIKey: "g", GeminiBaseURL: "https://generativelanguage.googleapis.com"}}
	if dGem.buildAdminChatPipeline() == nil {
		t.Error("a Gemini key should enable the admin chat")
	}

	// OPENROUTER_API_KEY without SEMIDX_CHAT_MODEL: OpenRouter has no default
	// model, so no chat provider resolves and chat must be unavailable — the
	// legacy non-agent chat.Chain fallback that hid this is gone. The SPA's 503
	// message guides the user to also set SEMIDX_CHAT_MODEL.
	dOR := &deps{emb: stubEmbedder{}, cfg: &config.Config{OpenRouterAPIKey: "or-key"}}
	if got := dOR.buildAdminChatPipeline(); got != nil {
		t.Errorf("OpenRouter key without SEMIDX_CHAT_MODEL must disable chat, got %T", got)
	}

	// nil cfg/emb → nil.
	if (&deps{}).buildAdminChatPipeline() != nil {
		t.Error("nil cfg/emb should yield nil chat")
	}
}

// multiModelDeps builds deps with an openai-compatible default chat provider
// plus a Gemini key backing an extra allowlist entry (SEMIDX_CHAT_MODELS).
func multiModelDeps() *deps {
	return &deps{
		emb: stubEmbedder{},
		cfg: &config.Config{
			ChatProvider:  "openai-compatible",
			ChatModel:     "deepseek-v4-flash-free",
			ChatAPIKey:    "k",
			ChatBaseURL:   "https://opencode.ai/zen/v1",
			GeminiAPIKey:  "g",
			GeminiBaseURL: "https://generativelanguage.googleapis.com",
			ChatModels:    []string{"google/gemini-2.5-flash", "malformed-entry"},
			AgentActions:  "off",
		},
	}
}

// TestAdminChatConfigContract asserts the factory's Config() matches the frozen
// GET /admin/api/chat/config contract fields.
func TestAdminChatConfigContract(t *testing.T) {
	p, ok := multiModelDeps().buildAdminChatPipeline().(*adminChat)
	if !ok {
		t.Fatal("expected the adminChat factory")
	}
	cfg := p.Config()
	if !cfg.Enabled || cfg.DefaultMode != "agent" || cfg.DefaultModel != "deepseek-v4-flash-free" {
		t.Errorf("config = %+v", cfg)
	}
	if len(cfg.Modes) != 2 || cfg.Modes[0] != "agent" || cfg.Modes[1] != "rag" {
		t.Errorf("modes = %v", cfg.Modes)
	}
	if cfg.AgentActions != "off" {
		t.Errorf("agent_actions = %q", cfg.AgentActions)
	}
	// The malformed SEMIDX_CHAT_MODELS entry is dropped; the default is first
	// and flagged, the Gemini extra follows.
	if len(cfg.Models) != 2 {
		t.Fatalf("models = %+v", cfg.Models)
	}
	if cfg.Models[0].ID != "deepseek-v4-flash-free" || !cfg.Models[0].Default ||
		cfg.Models[0].Provider != "openai-compatible" {
		t.Errorf("models[0] = %+v", cfg.Models[0])
	}
	if cfg.Models[1].ID != "gemini-2.5-flash" || cfg.Models[1].Provider != "google" || cfg.Models[1].Default {
		t.Errorf("models[1] = %+v", cfg.Models[1])
	}
}

// TestAdminChatBackendFor covers the per-request routing: default/agent mode →
// the tool-loop pipeline, rag mode → the FantasyPipeline, unknown mode/model →
// error; and the model cache returns the same resolved instance.
func TestAdminChatBackendFor(t *testing.T) {
	p, ok := multiModelDeps().buildAdminChatPipeline().(*adminChat)
	if !ok {
		t.Fatal("expected the adminChat factory")
	}
	ctx := context.Background()

	be, err := p.backendFor(ctx, webadmin.ChatOptions{})
	if err != nil {
		t.Fatalf("default mode: %v", err)
	}
	if _, ok := be.(*agentChatPipeline); !ok {
		t.Errorf("default mode backend = %T, want agentChatPipeline", be)
	}
	be, err = p.backendFor(ctx, webadmin.ChatOptions{Mode: "rag", Model: "gemini-2.5-flash"})
	if err != nil {
		t.Fatalf("rag mode: %v", err)
	}
	if _, ok := be.(*rag.FantasyPipeline); !ok {
		t.Errorf("rag mode backend = %T, want FantasyPipeline", be)
	}
	if _, err := p.backendFor(ctx, webadmin.ChatOptions{Mode: "weird"}); err == nil {
		t.Error("unknown mode must error (defense-in-depth behind the 400)")
	}
	if _, err := p.backendFor(ctx, webadmin.ChatOptions{Model: "not-listed"}); err == nil {
		t.Error("model outside the allowlist must error")
	}

	// Cache: resolving the same model id twice yields the same instance.
	m1, err := p.modelFor(ctx, "gemini-2.5-flash")
	if err != nil {
		t.Fatalf("modelFor: %v", err)
	}
	m2, err := p.modelFor(ctx, "gemini-2.5-flash")
	if err != nil {
		t.Fatalf("modelFor (cached): %v", err)
	}
	if m1 != m2 {
		t.Error("model cache must return the same resolved instance")
	}
}

// TestChatLLMFor: credentials resolve per allowlist entry — the explicit
// SEMIDX_CHAT_API_KEY belongs to the configured provider only and must not
// leak onto other providers, which fall back to their embedding keys.
func TestChatLLMFor(t *testing.T) {
	d := multiModelDeps()
	sel, ok := d.chatLLMFor(config.ChatModelSpec{Provider: "google", Model: "gemini-2.5-flash"})
	if !ok {
		t.Fatal("google entry must resolve via GEMINI_API_KEY")
	}
	if sel.APIKey != "g" || sel.Provider != "google" || sel.Model != "gemini-2.5-flash" {
		t.Errorf("sel = %+v (explicit chat key must not leak)", sel)
	}
	// Same provider as the configured one keeps the explicit key.
	sel, ok = d.chatLLMFor(config.ChatModelSpec{Provider: "openai-compatible", Model: "other"})
	if !ok || sel.APIKey != "k" || sel.BaseURL != "https://opencode.ai/zen/v1" {
		t.Errorf("configured-provider sel = %+v ok=%v", sel, ok)
	}
	// A provider with no credentials anywhere does not resolve.
	if _, ok := d.chatLLMFor(config.ChatModelSpec{Provider: "anthropic", Model: "claude-x"}); ok {
		t.Error("provider without any key must not resolve")
	}
}

// scriptedStreamModel is a fantasy.LanguageModel whose Stream replays one
// pre-programmed part sequence per call (step 1: tool call, step 2: answer).
type scriptedStreamModel struct {
	mu      sync.Mutex
	scripts [][]fantasy.StreamPart
	idx     int
}

func (m *scriptedStreamModel) Stream(_ context.Context, _ fantasy.Call) (fantasy.StreamResponse, error) {
	m.mu.Lock()
	var parts []fantasy.StreamPart
	if m.idx < len(m.scripts) {
		parts = m.scripts[m.idx]
		m.idx++
	}
	m.mu.Unlock()
	return func(yield func(fantasy.StreamPart) bool) {
		for _, p := range parts {
			if !yield(p) {
				return
			}
		}
	}, nil
}

func (m *scriptedStreamModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return &fantasy.Response{
		Content:      fantasy.ResponseContent{fantasy.TextContent{Text: "unused"}},
		FinishReason: fantasy.FinishReasonStop,
	}, nil
}

func (m *scriptedStreamModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, context.Canceled
}

func (m *scriptedStreamModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, context.Canceled
}

func (m *scriptedStreamModel) Provider() string { return "fake" }
func (m *scriptedStreamModel) Model() string    { return "fake-model" }

type echoToolInput struct {
	Q string `json:"q" description:"query"`
}

// collectChunks drains the stream channel with a deadline, failing the test if
// the channel does not close in time (goroutine leak guard).
func collectChunks(t *testing.T, ch <-chan chat.StreamChunk) []chat.StreamChunk {
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

// TestAgentChatPipeline_streamToolEvents drives the real Runner with a fake
// fantasy model that calls a tool, and asserts the tool call/result events
// flow through StreamAsk sanitized (secret args redacted, preview bounded)
// and correlated by the fantasy tool-call id.
func TestAgentChatPipeline_streamToolEvents(t *testing.T) {
	model := &scriptedStreamModel{scripts: [][]fantasy.StreamPart{
		{
			{Type: fantasy.StreamPartTypeToolCall, ID: "call_abc", ToolCallName: "echo",
				ToolCallInput: `{"q":"auth","api_key":"sk-super-secret"}`},
			{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls},
		},
		{
			{Type: fantasy.StreamPartTypeTextStart, ID: "t1"},
			{Type: fantasy.StreamPartTypeTextDelta, ID: "t1", Delta: "the answer"},
			{Type: fantasy.StreamPartTypeTextEnd, ID: "t1"},
			{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop},
		},
	}}
	echo := fantasy.NewParallelAgentTool("echo", "echo tool",
		func(_ context.Context, _ echoToolInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			return fantasy.NewTextResponse(strings.Repeat("r", 600)), nil
		})
	p := &agentChatPipeline{runner: agent.NewRunner(model, []fantasy.AgentTool{echo}, agent.RunnerConfig{})}

	ch, _, gotModel, _, err := p.StreamAsk(context.Background(), "q", "demo", nil)
	if err != nil {
		t.Fatalf("StreamAsk: %v", err)
	}
	if gotModel != "fake-model" {
		t.Errorf("model = %q", gotModel)
	}
	chunks := collectChunks(t, ch)

	var call, result *chat.ToolEvent
	var text strings.Builder
	var done *chat.StreamChunk
	for i, c := range chunks {
		switch {
		case c.Tool != nil && c.Tool.Kind == chat.ToolEventCall:
			if call != nil {
				t.Fatal("duplicate tool call event")
			}
			if result != nil {
				t.Fatal("tool result arrived before the tool call")
			}
			call = c.Tool
		case c.Tool != nil && c.Tool.Kind == chat.ToolEventResult:
			result = c.Tool
		case c.Done:
			done = &chunks[i]
		default:
			text.WriteString(c.Content)
		}
	}
	if call == nil || result == nil || done == nil {
		t.Fatalf("missing events: call=%v result=%v done=%v (chunks=%+v)", call, result, done, chunks)
	}
	if call.ID != "call_abc" || call.Name != "echo" {
		t.Errorf("tool call = %+v", call)
	}
	args := string(call.Args)
	if !strings.Contains(args, `"api_key":"[redacted]"`) || !strings.Contains(args, `"q":"auth"`) {
		t.Errorf("args must be sanitized, got %s", args)
	}
	if result.ID != "call_abc" || result.Name != "echo" || result.IsError {
		t.Errorf("tool result = %+v", result)
	}
	if len([]rune(result.Preview)) != toolPreviewMaxRunes || !result.Truncated {
		t.Errorf("preview must be truncated to %d runes (got %d, truncated=%v)",
			toolPreviewMaxRunes, len([]rune(result.Preview)), result.Truncated)
	}
	if result.ElapsedMS < 0 {
		t.Errorf("elapsed = %d, want >= 0", result.ElapsedMS)
	}
	if text.String() != "the answer" {
		t.Errorf("streamed text = %q", text.String())
	}
	if done.Err != "" {
		t.Errorf("clean stream must not carry Err, got %q", done.Err)
	}
}

// errStreamModel fails the stream immediately with a provider-looking error.
type errStreamModel struct{ scriptedStreamModel }

func (m *errStreamModel) Stream(_ context.Context, _ fantasy.Call) (fantasy.StreamResponse, error) {
	return func(yield func(fantasy.StreamPart) bool) {
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeError,
			Error: context.DeadlineExceeded})
	}, nil
}

// TestAgentChatPipeline_streamErrorIsGeneric: a failed stream must surface only
// the generic backend message on the terminal chunk, never the provider error.
func TestAgentChatPipeline_streamErrorIsGeneric(t *testing.T) {
	p := &agentChatPipeline{runner: agent.NewRunner(&errStreamModel{}, nil, agent.RunnerConfig{})}
	ch, _, _, _, err := p.StreamAsk(context.Background(), "q", "", nil)
	if err != nil {
		t.Fatalf("StreamAsk: %v", err)
	}
	chunks := collectChunks(t, ch)
	if len(chunks) == 0 {
		t.Fatal("expected a terminal chunk")
	}
	last := chunks[len(chunks)-1]
	if !last.Done {
		t.Fatalf("last chunk must be terminal, got %+v", last)
	}
	if last.Err != chatBackendErrMsg {
		t.Errorf("Err = %q, want the generic message %q", last.Err, chatBackendErrMsg)
	}
	if strings.Contains(last.Err, context.DeadlineExceeded.Error()) {
		t.Errorf("provider error leaked into the stream: %q", last.Err)
	}
}

// blockingStreamModel blocks its stream until the request context is canceled,
// then reports the cancellation as a stream error (like a real provider).
type blockingStreamModel struct {
	scriptedStreamModel
	started   chan struct{}
	startOnce sync.Once
}

func (m *blockingStreamModel) Stream(ctx context.Context, _ fantasy.Call) (fantasy.StreamResponse, error) {
	return func(yield func(fantasy.StreamPart) bool) {
		m.startOnce.Do(func() { close(m.started) })
		<-ctx.Done()
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeError, Error: ctx.Err()})
	}, nil
}

// TestAgentChatPipeline_cancelClosesStream is the cancellation regression:
// canceling the request context must close the stream channel promptly (no
// stuck goroutine) and must NOT surface a backend-error chunk — the client
// went away, nothing failed.
func TestAgentChatPipeline_cancelClosesStream(t *testing.T) {
	model := &blockingStreamModel{started: make(chan struct{})}
	p := &agentChatPipeline{runner: agent.NewRunner(model, nil, agent.RunnerConfig{})}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _, _, _, err := p.StreamAsk(ctx, "q", "demo", nil)
	if err != nil {
		t.Fatalf("StreamAsk: %v", err)
	}

	select {
	case <-model.started: // the agent loop is inside the provider stream
	case <-time.After(10 * time.Second):
		t.Fatal("stream never reached the model")
	}
	cancel()

	// The channel must close (collectChunks enforces the timeout) and no chunk
	// after cancellation may carry a backend error.
	for _, c := range collectChunks(t, ch) {
		if c.Err != "" {
			t.Errorf("cancellation must not surface a backend error, got %q", c.Err)
		}
	}
}
