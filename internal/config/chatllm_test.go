package config

import "testing"

func TestResolveChatLLM_autoDetect(t *testing.T) {
	// Gemini key → google with the default model, honoring temperature.
	c := &Config{GeminiAPIKey: "g", GeminiBaseURL: "https://gem", ChatTemperature: 0.3}
	sel, ok := c.ResolveChatLLM()
	if !ok {
		t.Fatal("expected a usable selection from a Gemini key")
	}
	if sel.Provider != "google" || sel.Model != "gemini-2.5-flash" || sel.APIKey != "g" {
		t.Errorf("auto google = %+v", sel)
	}

	// OpenRouter without an explicit model is not usable (no safe default).
	c = &Config{OpenRouterAPIKey: "o"}
	if _, ok := c.ResolveChatLLM(); ok {
		t.Error("openrouter without a model must not be usable")
	}
	// OpenRouter with a model → usable.
	c = &Config{OpenRouterAPIKey: "o", ChatModel: "x/y"}
	sel, ok = c.ResolveChatLLM()
	if !ok || sel.Provider != "openrouter" || sel.Model != "x/y" {
		t.Errorf("openrouter with model = %+v ok=%v", sel, ok)
	}

	// Nothing configured → not usable.
	if _, ok := (&Config{}).ResolveChatLLM(); ok {
		t.Error("empty config must not yield a chat provider")
	}
}

func TestResolveChatLLM_explicitOpenAICompat(t *testing.T) {
	// OpenCode Zen via the openai-compatible provider.
	c := &Config{
		ChatProvider: "openai-compatible",
		ChatBaseURL:  "https://opencode.ai/zen/v1",
		ChatAPIKey:   "zen-key",
		ChatModel:    "deepseek-v4-flash-free",
	}
	sel, ok := c.ResolveChatLLM()
	if !ok {
		t.Fatal("expected a usable openai-compatible selection")
	}
	if sel.Provider != "openai-compatible" || sel.BaseURL != "https://opencode.ai/zen/v1" || sel.Model != "deepseek-v4-flash-free" {
		t.Errorf("zen selection = %+v", sel)
	}

	// openai-compatible without a BaseURL is not usable.
	c = &Config{ChatProvider: "openai-compatible", ChatModel: "m", ChatAPIKey: "k"}
	if _, ok := c.ResolveChatLLM(); ok {
		t.Error("openai-compatible without BaseURL must not be usable")
	}
}

func TestResolveChatLLM_explicitGoogleFallsBackToEmbeddingKey(t *testing.T) {
	// Explicit google provider but no ChatAPIKey → use the Gemini embedding key.
	c := &Config{ChatProvider: "google", GeminiAPIKey: "gem", GeminiBaseURL: "https://gem"}
	sel, ok := c.ResolveChatLLM()
	if !ok {
		t.Fatal("expected usable google selection via embedding key fallback")
	}
	if sel.APIKey != "gem" || sel.BaseURL != "https://gem" || sel.Model != "gemini-2.5-flash" {
		t.Errorf("google fallback = %+v", sel)
	}
}
