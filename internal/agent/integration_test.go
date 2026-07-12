package agent

import (
	"os"
	"strings"
	"testing"

	"charm.land/fantasy"

	"github.com/lgldsilva/semidx/internal/llm"
	"github.com/lgldsilva/semidx/internal/store"
)

// Live integration tests against OpenCode Zen (an OpenAI-compatible gateway).
// They are gated on SEMIDX_ZEN_API_KEY so CI and offline runs skip cleanly.
// Defaults target the free DeepSeek model; override via the SEMIDX_ZEN_* env.
const (
	zenDefaultBaseURL = "https://opencode.ai/zen/v1"
	zenDefaultModel   = "deepseek-v4-flash-free"
)

// zenModel resolves a live OpenCode Zen model, or skips when no key is set.
func zenModel(t *testing.T) fantasy.LanguageModel {
	t.Helper()
	key := os.Getenv("SEMIDX_ZEN_API_KEY")
	if key == "" {
		t.Skip("set SEMIDX_ZEN_API_KEY to run OpenCode Zen integration tests")
	}
	baseURL := os.Getenv("SEMIDX_ZEN_BASE_URL")
	if baseURL == "" {
		baseURL = zenDefaultBaseURL
	}
	model := os.Getenv("SEMIDX_ZEN_MODEL")
	if model == "" {
		model = zenDefaultModel
	}
	m, err := llm.ResolveModel(t.Context(), llm.ProviderConfig{
		Type:    llm.ProviderOpenAICompat,
		APIKey:  key,
		BaseURL: baseURL,
	}, model)
	if err != nil {
		t.Fatalf("resolve Zen model: %v", err)
	}
	return m
}

// TestIntegration_Zen_plainAnswer checks the provider + Runner produce a
// non-empty answer from a real model with no tools.
func TestIntegration_Zen_plainAnswer(t *testing.T) {
	r := NewRunner(zenModel(t), nil, RunnerConfig{
		SystemPrompt: "You are a terse assistant. Answer in one short sentence.",
		MaxSteps:     3,
	})
	ans, err := r.Ask(t.Context(), "Reply with the single word: pong", nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if strings.TrimSpace(ans.Content) == "" {
		t.Fatal("expected a non-empty answer from the model")
	}
	t.Logf("Zen answer: %q (model %s)", ans.Content, ans.Model)
}

// TestIntegration_Zen_toolCall checks the end-to-end tool-calling loop: a real
// model is given the list_projects tool and must call it to answer.
func TestIntegration_Zen_toolCall(t *testing.T) {
	fs := newFakeSearchStore()
	fs.addProject(&store.Project{
		Name: "acme-api", Identity: "id-acme", SourceType: "git", Status: "ready", Model: "m1",
	})
	tools := ReadTools(nil, fs, nil) // index_status + list_projects

	r := NewRunner(zenModel(t), tools, RunnerConfig{
		SystemPrompt: "You answer questions about indexed projects. When asked what is indexed, call the list_projects tool and report the names.",
		MaxSteps:     5,
	})
	ans, err := r.Ask(t.Context(), "Which projects are indexed? Use a tool to find out.", nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	calledListProjects := false
	for _, tc := range ans.Trace {
		if tc.Tool == "list_projects" {
			calledListProjects = true
		}
	}
	if !calledListProjects {
		t.Errorf("expected the model to call list_projects; trace=%v answer=%q", ans.Trace, ans.Content)
	}
	t.Logf("Zen tool-call answer: %q (trace=%v)", ans.Content, ans.Trace)
}
