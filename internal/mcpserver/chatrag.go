package mcpserver

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/chat"
	"github.com/lgldsilva/semidx/internal/rag"
)

// ragAsker is the Ask surface shared by rag.FantasyPipeline (and historically
// rag.Pipeline). MCP semantic_ask stays single-turn (history nil).
type ragAsker interface {
	Ask(ctx context.Context, question, project string, history []chat.Message) (*rag.Answer, error)
}

// chatRAGBackend wraps a Backend and a RAG Asker, providing the Ask method
// by combining search with an LLM call. Backend methods (Search, Projects,
// Reindex) are auto-delegated to the wrapped backend via embedding.
type chatRAGBackend struct {
	Backend
	pipeline ragAsker
}

// NewChatRAGBackend creates a Backend that also implements AskBackend by
// wrapping an existing Backend with a RAG pipeline (typically Fantasy).
func NewChatRAGBackend(b Backend, pipeline ragAsker) Backend {
	return &chatRAGBackend{Backend: b, pipeline: pipeline}
}

// Ask runs one RAG turn: retrieves chunks from the index, builds context, and
// sends the prompt to the chat LLM. topK is advisory — the pipeline uses its
// own configured TopK (set at creation time).
func (b *chatRAGBackend) Ask(ctx context.Context, project, question string, topK int) (*AskOutput, error) {
	// history is nil because the MCP tool is stateless (single-turn).
	answer, err := b.pipeline.Ask(ctx, question, project, nil)
	if err != nil {
		return nil, err
	}
	sources := make([]AskSource, len(answer.Sources))
	for i, s := range answer.Sources {
		sources[i] = AskSource{
			Path:      s.File,
			StartLine: s.StartLine,
			EndLine:   s.EndLine,
			Score:     s.Score,
			Keyword:   s.Keyword,
		}
	}
	return &AskOutput{
		Answer:   answer.Content,
		Sources:  sources,
		Model:    answer.Model,
		Fallback: answer.Fallback,
	}, nil
}

// Unwrap exposes the wrapped backend so tool gating can see its extra
// capabilities (GitBackend, MultiSearchBackend) through the ask wrapper.
func (b *chatRAGBackend) Unwrap() Backend { return b.Backend }

// Compile-time interface assertion.
var _ AskBackend = (*chatRAGBackend)(nil)

// ---------------------------------------------------------------------------
// Agentic ask backend — wraps an agent.Agent for the MCP semantic_ask tool.
// ---------------------------------------------------------------------------

// NewAgenticAskBackend creates a Backend that delegates Ask to the fantasy
// agent Runner. Other Backend methods (Search, Projects, Status, Reindex) fall
// through to the wrapped backend. Use when the chat LLM supports tool calling.
func NewAgenticAskBackend(b Backend, runner *agent.Runner) Backend {
	return &agenticAskBackend{Backend: b, runner: runner}
}

type agenticAskBackend struct {
	Backend
	runner *agent.Runner
}

// Ask runs the agent loop instead of the simple RAG pipeline.
// The agent has access to semantic_search, repo tools, etc. The MCP tool is
// single-turn (no history), so tool memory lives only within this loop.
func (b *agenticAskBackend) Ask(ctx context.Context, project, question string, _ int) (*AskOutput, error) {
	// Bind the turn to the project so semantic_search is scoped by contract —
	// the prompt hint alone (below) is not a guarantee the model honors.
	if project != "" {
		ctx = agent.ContextWithScope(ctx, agent.SearchScope{Project: project})
	}
	fullQuestion := question
	if project != "" {
		fullQuestion = fmt.Sprintf("[project: %s] %s", project, question)
	}
	answer, err := b.runner.Ask(ctx, fullQuestion, nil)
	if err != nil {
		return nil, fmt.Errorf("agent ask failed: %w", err)
	}

	// Observability: token accounting per ask (stderr; stdout is the MCP
	// protocol channel).
	slog.Info("agent ask complete",
		"model", answer.Model,
		"tool_calls", len(answer.Trace),
		"input_tokens", answer.Usage.InputTokens,
		"output_tokens", answer.Usage.OutputTokens,
		"total_tokens", answer.Usage.TotalTokens,
	)

	return &AskOutput{
		Answer:  answer.Content,
		Sources: sourcesFromTrace(answer.Trace),
		Model:   answer.Model,
	}, nil
}

// sourcesFromTrace reconstructs real AskSource entries from the semantic_search
// tool results captured in the agent trace, via the shared agent parser (deduped
// by file+line span, highest score kept).
func sourcesFromTrace(trace []agent.ToolCallRecord) []AskSource {
	hits, _ := agent.SourcesFromTrace(trace)
	out := make([]AskSource, 0, len(hits))
	for _, h := range hits {
		out = append(out, AskSource{
			Path:      h.File,
			StartLine: h.StartLine,
			EndLine:   h.EndLine,
			Score:     h.Score,
			Keyword:   h.Keyword,
		})
	}
	return out
}

// Unwrap exposes the wrapped backend so tool gating can see its git/multi-search
// capabilities through the agentic ask wrapper.
func (b *agenticAskBackend) Unwrap() Backend { return b.Backend }

// Compile-time interface assertion.
var _ AskBackend = (*agenticAskBackend)(nil)
