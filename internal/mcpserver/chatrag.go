package mcpserver

import (
	"context"

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

// Compile-time interface assertion.
var _ AskBackend = (*chatRAGBackend)(nil)
