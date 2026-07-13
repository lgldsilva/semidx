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
	// Observability: a real provider reports token usage.
	if ans.Usage.TotalTokens == 0 && ans.Usage.InputTokens == 0 && ans.Usage.OutputTokens == 0 {
		t.Errorf("expected non-zero token usage from a live model, got %+v", ans.Usage)
	}
	t.Logf("Zen answer: %q (model %s, usage %+v)", ans.Content, ans.Model, ans.Usage)
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

// TestIntegration_Zen_multiTurnToolMemory is the decisive live check for F5.A:
// turn 1 calls a tool; the assistant tool_call and its result are carried into
// the conversation. Turn 2 runs a Runner with NO tools at all, yet must answer
// from the earlier tool output — proving the multi-turn tool memory works and
// isn't just a coincidental re-derivation.
func TestIntegration_Zen_multiTurnToolMemory(t *testing.T) {
	model := zenModel(t)
	fs := newFakeSearchStore()
	fs.addProject(&store.Project{
		Name: "acme-api", Identity: "id-acme", SourceType: "git", Status: "ready", Model: "m1",
	})

	// Turn 1: the agent discovers the project via the tool.
	toolRunner := NewRunner(model, ReadTools(nil, fs, nil), RunnerConfig{
		SystemPrompt: "You answer questions about indexed projects. Call list_projects when asked what is indexed, then report the names.",
		MaxSteps:     5,
	})
	q1 := "Which projects are indexed? Use a tool to find out."
	ans1, err := toolRunner.Ask(t.Context(), q1, nil)
	if err != nil {
		t.Fatalf("turn 1 Ask: %v", err)
	}
	if len(ans1.Messages) == 0 {
		t.Fatal("turn 1 must return the turn messages for multi-turn memory")
	}
	// The trace must carry the real tool output (not just the call args).
	var listResult string
	for _, tc := range ans1.Trace {
		if tc.Tool == "list_projects" {
			listResult = tc.Result
		}
	}
	if !strings.Contains(listResult, "acme-api") {
		t.Fatalf("turn 1 tool result should contain the project name, got %q", listResult)
	}

	// Persist the full turn, exactly like the REPL does.
	convo := NewConversation(10)
	convo.AddUser(q1)
	convo.AddTurnMessages(ans1.Messages)

	// Turn 2: a Runner with NO tools. The only way it can name the project is
	// from the conversation history carried over from turn 1.
	noToolRunner := NewRunner(model, nil, RunnerConfig{
		SystemPrompt: "Answer strictly from the conversation so far. Do not ask to use tools.",
		MaxSteps:     2,
	})
	ans2, err := noToolRunner.Ask(t.Context(),
		"What is the exact name of the indexed project we just discussed? Reply with only the name.",
		convo.Messages())
	if err != nil {
		t.Fatalf("turn 2 Ask: %v", err)
	}
	if !strings.Contains(strings.ToLower(ans2.Content), "acme-api") {
		t.Errorf("turn 2 answer should name acme-api from memory, got %q", ans2.Content)
	}
	t.Logf("Zen multi-turn memory answer: %q", ans2.Content)
}
