package main

import (
	"context"
	"testing"

	"github.com/lgldsilva/semidx/internal/config"
	"github.com/lgldsilva/semidx/internal/embed"
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
	if _, ok := p.(*agentChatPipeline); !ok {
		t.Errorf("want the agent chat pipeline, got %T", p)
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

	// nil cfg/emb → nil.
	if (&deps{}).buildAdminChatPipeline() != nil {
		t.Error("nil cfg/emb should yield nil chat")
	}
}
