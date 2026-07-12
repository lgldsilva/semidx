// Package embed abstracts text-embedding providers behind a single Embedder
// interface and composes them into a fallback chain or parallel pool.
package embed

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/attribute"

	"github.com/lgldsilva/semidx/internal/observ"
)

// ParallelEmbedder distributes embedding requests across N sub-embedders using
// round-robin. Each sub-embedder can be a single provider (OllamaClient) or a
// fallback chain (ChainEmbedder). This enables pooling multiple Ollama
// instances (local + remote GPU boxes) alongside cloud chains (Gemini →
// OpenRouter → OllamaCloud) as one virtual embedder.
//
// In normal mode, entries are round-robined strictly. When the context is
// force-local (privacy mode), entries that fail (typically non-local cloud
// chains) are retried against the remaining entries until one succeeds.
type ParallelEmbedder struct {
	entries []Embedder
	mu      sync.Mutex
	next    int
}

// NewParallelEmbedder creates a pool over entries. At least one entry is
// required.
func NewParallelEmbedder(entries []Embedder) *ParallelEmbedder {
	return &ParallelEmbedder{entries: entries}
}

func (p *ParallelEmbedder) Embed(ctx context.Context, model string, inputs ...string) ([][]float32, error) {
	ctx, span := observ.StartSpan(ctx, "embed.ParallelEmbedder.Embed")
	defer span.End()
	span.SetAttributes(attribute.String("model", model), attribute.Int("inputs", len(inputs)))

	start := p.nextEntry()
	res, err := p.entries[start].Embed(ctx, model, inputs...)
	if err == nil || !isForceLocal(ctx) {
		// Normal mode: return immediately (don't retry, trust round-robin).
		// Privacy mode: entry failed (likely non-local cloud) — try remaining.
		return res, err
	}
	return p.embedFallback(ctx, model, inputs, start)
}

func (p *ParallelEmbedder) EmbedSingle(ctx context.Context, model, text string) ([]float32, error) {
	start := p.nextEntry()
	res, err := p.entries[start].EmbedSingle(ctx, model, text)
	if err == nil || !isForceLocal(ctx) {
		return res, err
	}
	// Privacy fallback for single embedding.
	for i := 1; i < len(p.entries); i++ {
		idx := (start + i) % len(p.entries)
		res, err = p.entries[idx].EmbedSingle(ctx, model, text)
		if err == nil {
			return res, nil
		}
	}
	return nil, fmt.Errorf("parallel: all %d entries failed with force-local: %w", len(p.entries), err)
}

func (p *ParallelEmbedder) embedFallback(ctx context.Context, model string, inputs []string, start int) ([][]float32, error) {
	var lastErr error
	for i := 1; i < len(p.entries); i++ {
		idx := (start + i) % len(p.entries)
		res, err := p.entries[idx].Embed(ctx, model, inputs...)
		if err == nil {
			return res, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("parallel: all %d entries failed with force-local: %w", len(p.entries), lastErr)
}

func (p *ParallelEmbedder) ModelInfo(ctx context.Context, model string) (*ModelInfo, error) {
	return p.entries[0].ModelInfo(ctx, model)
}

func (p *ParallelEmbedder) ListModels(ctx context.Context) ([]string, error) {
	return p.entries[0].ListModels(ctx)
}

func (p *ParallelEmbedder) nextEntry() int {
	p.mu.Lock()
	idx := p.next
	p.next = (p.next + 1) % len(p.entries)
	p.mu.Unlock()
	return idx
}

var _ Embedder = (*ParallelEmbedder)(nil)
