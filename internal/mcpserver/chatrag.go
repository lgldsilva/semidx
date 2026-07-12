package mcpserver

import (
	"context"
	"fmt"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/rag"
)

// chatRAGBackend wraps a Backend and a RAG Pipeline, providing the Ask method
// by combining search (via the rag.Pipeline) with an LLM call.
// Backend methods (Search, Projects, Reindex) are auto-delegated to the
// wrapped backend via embedding.
type chatRAGBackend struct {
	Backend
	pipeline *rag.Pipeline
}

// NewChatRAGBackend creates a Backend that also implements AskBackend
// by wrapping an existing Backend with a RAG pipeline.
func NewChatRAGBackend(b Backend, pipeline *rag.Pipeline) Backend {
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
// The agent has access to semantic_search, repo tools, etc.
func (b *agenticAskBackend) Ask(ctx context.Context, project, question string, _ int) (*AskOutput, error) {
	// Prepend project context to the question so the agent knows the scope.
	fullQuestion := question
	if project != "" {
		fullQuestion = fmt.Sprintf("[project: %s] %s", project, question)
	}
	answer, err := b.runner.Ask(ctx, fullQuestion, nil)
	if err != nil {
		return nil, fmt.Errorf("agent ask failed: %w", err)
	}

	// Build sources from tool trace where possible.
	var sources []AskSource
	for _, tc := range answer.Trace {
		if tc.Tool == "semantic_search" && tc.Error == "" {
			// Try to extract sources from structured search results.
			// At minimum, note that search was used.
			sources = append(sources, AskSource{
				Path:    fmt.Sprintf("tool://%s", tc.Tool),
				Score:   1.0,
				Keyword: false,
			})
		}
	}
	if sources == nil {
		sources = []AskSource{}
	}

	return &AskOutput{
		Answer:  answer.Content,
		Sources: sources,
		Model:   answer.Model,
	}, nil
}

// Unwrap exposes the wrapped backend so tool gating can see its git/multi-search
// capabilities through the agentic ask wrapper.
func (b *agenticAskBackend) Unwrap() Backend { return b.Backend }

// Compile-time interface assertion.
var _ AskBackend = (*agenticAskBackend)(nil)
