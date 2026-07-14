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

	// Groq with a model → usable (same shape as OpenRouter).
	c = &Config{GroqAPIKey: "q", ChatModel: "llama-3.3-70b"}
	sel, ok = c.ResolveChatLLM()
	if !ok || sel.Provider != "groq" || sel.Model != "llama-3.3-70b" || sel.APIKey != "q" {
		t.Errorf("groq with model = %+v ok=%v", sel, ok)
	}

	// Nothing configured → not usable.
	if _, ok := (&Config{}).ResolveChatLLM(); ok {
		t.Error("empty config must not yield a chat provider")
	}
}

func TestResolveChatLLM_autoDetectAnthropic(t *testing.T) {
	// Anthropic key alone → anthropic with the default model.
	c := &Config{AnthropicAPIKey: "ant", ChatTemperature: 0.3}
	sel, ok := c.ResolveChatLLM()
	if !ok {
		t.Fatal("expected a usable selection from an Anthropic key")
	}
	if sel.Provider != "anthropic" || sel.Model != defaultAnthropicChatModel || sel.APIKey != "ant" {
		t.Errorf("auto anthropic = %+v", sel)
	}

	// Anthropic + Gemini → anthropic wins: the Gemini key is an embedding key
	// reused for chat, while ANTHROPIC_API_KEY is chat-only intent.
	c = &Config{AnthropicAPIKey: "ant", GeminiAPIKey: "g", GeminiBaseURL: "https://gem"}
	sel, ok = c.ResolveChatLLM()
	if !ok || sel.Provider != "anthropic" || sel.APIKey != "ant" {
		t.Errorf("anthropic must win over gemini, got %+v ok=%v", sel, ok)
	}

	// An explicit chat model overrides the anthropic default.
	c = &Config{AnthropicAPIKey: "ant", ChatModel: "claude-opus-4-8"}
	sel, _ = c.ResolveChatLLM()
	if sel.Model != "claude-opus-4-8" {
		t.Errorf("anthropic model override = %q", sel.Model)
	}
}

func TestResolveChatLLM_explicitAnthropic(t *testing.T) {
	// Explicit anthropic provider with a custom model; the Anthropic key fills
	// the empty ChatAPIKey.
	c := &Config{ChatProvider: "anthropic", AnthropicAPIKey: "ant", ChatModel: "claude-opus-4-8"}
	sel, ok := c.ResolveChatLLM()
	if !ok {
		t.Fatal("expected a usable explicit anthropic selection")
	}
	if sel.Provider != "anthropic" || sel.Model != "claude-opus-4-8" || sel.APIKey != "ant" {
		t.Errorf("explicit anthropic = %+v", sel)
	}

	// Without SEMIDX_CHAT_MODEL the default model applies; an explicit
	// SEMIDX_CHAT_API_KEY wins over ANTHROPIC_API_KEY.
	c = &Config{ChatProvider: "anthropic", AnthropicAPIKey: "ant", ChatAPIKey: "chat"}
	sel, _ = c.ResolveChatLLM()
	if sel.Model != defaultAnthropicChatModel || sel.APIKey != "chat" {
		t.Errorf("explicit anthropic defaults = %+v", sel)
	}
}

func TestResolveChatLLM_explicitGoogleBeatsAnthropicAutoDetect(t *testing.T) {
	// SEMIDX_CHAT_PROVIDER=google with both keys present → google, giving users
	// affected by the anthropic-first auto-detect their old default back.
	c := &Config{ChatProvider: "google", AnthropicAPIKey: "ant", GeminiAPIKey: "gem", GeminiBaseURL: "https://gem"}
	sel, ok := c.ResolveChatLLM()
	if !ok {
		t.Fatal("expected a usable google selection")
	}
	if sel.Provider != "google" || sel.APIKey != "gem" || sel.Model != "gemini-2.5-flash" {
		t.Errorf("explicit google = %+v", sel)
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

func TestResolveChatLLM_explicitCopilot(t *testing.T) {
	// Copilot with a dedicated token → usable with the default model.
	c := &Config{ChatProvider: "copilot", CopilotToken: "gh-copilot"}
	sel, ok := c.ResolveChatLLM()
	if !ok {
		t.Fatal("expected a usable copilot selection")
	}
	if sel.Provider != "copilot" || sel.APIKey != "gh-copilot" || sel.Model != "gpt-4o" {
		t.Errorf("copilot selection = %+v", sel)
	}

	// An explicit chat model overrides the default; the token still fills the key.
	c = &Config{ChatProvider: "copilot", CopilotToken: "gh", ChatModel: "claude-3.5-sonnet"}
	sel, _ = c.ResolveChatLLM()
	if sel.Model != "claude-3.5-sonnet" {
		t.Errorf("copilot model override = %q", sel.Model)
	}

	// Copilot without any token is not usable (no GitHub token to exchange).
	c = &Config{ChatProvider: "copilot"}
	if _, ok := c.ResolveChatLLM(); ok {
		t.Error("copilot without a token must not be usable")
	}
}

func TestResolveChatLLM_explicitGroqAndOpenRouter(t *testing.T) {
	// Explicit groq: the embedding key fills the empty ChatAPIKey; there is no
	// default model, so a model is required for the selection to be usable.
	c := &Config{ChatProvider: "groq", GroqAPIKey: "q", GroqBaseURL: "https://groq"}
	if _, ok := c.ResolveChatLLM(); ok {
		t.Error("explicit groq without a model must not be usable")
	}
	c.ChatModel = "llama-3.3-70b"
	sel, ok := c.ResolveChatLLM()
	if !ok || sel.Provider != "groq" || sel.APIKey != "q" || sel.BaseURL != "https://groq" {
		t.Errorf("explicit groq = %+v ok=%v", sel, ok)
	}

	// Explicit openrouter behaves the same way.
	c = &Config{ChatProvider: "openrouter", OpenRouterAPIKey: "o", OpenRouterBaseURL: "https://or", ChatModel: "x/y"}
	sel, ok = c.ResolveChatLLM()
	if !ok || sel.Provider != "openrouter" || sel.APIKey != "o" || sel.BaseURL != "https://or" {
		t.Errorf("explicit openrouter = %+v ok=%v", sel, ok)
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
