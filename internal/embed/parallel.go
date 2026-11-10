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
	// remote[i] marks entries that send data off the machine. They are never
	// selected for privacy-restricted requests (global privacy or a
	// force-local context), so sensitive chunks cannot be round-robined onto
	// a cloud lane.
	remote  []bool
	privacy bool
	mu      sync.Mutex
	next    int
}

// NewParallelEmbedder creates a pool over entries, all treated as local. At
// least one entry is required.
func NewParallelEmbedder(entries []Embedder) *ParallelEmbedder {
	return &ParallelEmbedder{entries: entries, remote: make([]bool, len(entries))}
}

// poolEntry is one lane of a locality-aware pool.
type poolEntry struct {
	embedder Embedder
	remote   bool
}

func newLocalityPool(lanes []poolEntry, privacy bool) *ParallelEmbedder {
	p := &ParallelEmbedder{privacy: privacy}
	for _, l := range lanes {
		p.entries = append(p.entries, l.embedder)
		p.remote = append(p.remote, l.remote)
	}
	return p
}

func (p *ParallelEmbedder) restricted(ctx context.Context) bool {
	return p.privacy || isForceLocal(ctx)
}

// candidates returns entry indexes in round-robin order starting at the next
// lane. Normal requests get only that lane (strict round-robin, no retry);
// restricted requests get every local lane so one failing instance falls
// through to the others.
func (p *ParallelEmbedder) candidates(ctx context.Context) []int {
	start := p.nextEntry()
	if !p.restricted(ctx) {
		return []int{start}
	}
	out := make([]int, 0, len(p.entries))
	for i := range p.entries {
		if idx := (start + i) % len(p.entries); !p.remote[idx] {
			out = append(out, idx)
		}
	}
	return out
}

func errNoLocalLane(n int) error {
	return fmt.Errorf("parallel: privacy-restricted request but none of %d entries is local", n)
}

func (p *ParallelEmbedder) Embed(ctx context.Context, model string, inputs ...string) ([][]float32, error) {
	ctx, span := observ.StartSpan(ctx, "embed.ParallelEmbedder.Embed")
	defer span.End()
	span.SetAttributes(attribute.String("model", model), attribute.Int("inputs", len(inputs)))

	return tryLanes(ctx, p, func(e Embedder) ([][]float32, error) { return e.Embed(ctx, model, inputs...) })
}

func (p *ParallelEmbedder) EmbedSingle(ctx context.Context, model, text string) ([]float32, error) {
	return tryLanes(ctx, p, func(e Embedder) ([]float32, error) { return e.EmbedSingle(ctx, model, text) })
}

// tryLanes runs call against the candidate lanes until one succeeds.
func tryLanes[T any](ctx context.Context, p *ParallelEmbedder, call func(Embedder) (T, error)) (T, error) {
	var zero T
	lanes := p.candidates(ctx)
	if len(lanes) == 0 {
		return zero, errNoLocalLane(len(p.entries))
	}
	var lastErr error
	for _, idx := range lanes {
		res, err := call(p.entries[idx])
		if err == nil {
			return res, nil
		}
		lastErr = err
	}
	if len(lanes) == 1 && !p.restricted(ctx) {
		return zero, lastErr
	}
	return zero, fmt.Errorf("parallel: all %d entries failed with force-local: %w", len(lanes), lastErr)
}

// ModelInfo tries entries in order and returns the first successful answer, so
// one dead pool member cannot hide the dims the others know.
func (p *ParallelEmbedder) ModelInfo(ctx context.Context, model string) (*ModelInfo, error) {
	var lastErr error = errNoLocalLane(len(p.entries))
	for i, e := range p.entries {
		if p.remote[i] && p.restricted(ctx) {
			continue
		}
		info, err := e.ModelInfo(ctx, model)
		if err == nil {
			return info, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("parallel: all %d entries failed to get model info: %w", len(p.entries), lastErr)
}

// ListModels returns the deduplicated union across all entries (same contract
// as ChainEmbedder.ListModels); entries that error are skipped.
func (p *ParallelEmbedder) ListModels(ctx context.Context) ([]string, error) {
	var models []string
	seen := make(map[string]bool)
	for _, e := range p.entries {
		list, err := e.ListModels(ctx)
		if err != nil {
			continue
		}
		for _, m := range list {
			if !seen[m] {
				seen[m] = true
				models = append(models, m)
			}
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("parallel: no embedding model available")
	}
	return models, nil
}

func (p *ParallelEmbedder) nextEntry() int {
	p.mu.Lock()
	idx := p.next
	p.next = (p.next + 1) % len(p.entries)
	p.mu.Unlock()
	return idx
}

var _ Embedder = (*ParallelEmbedder)(nil)
