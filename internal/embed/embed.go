// Package embed abstracts text-embedding providers behind a single Embedder
// interface and composes them into a fallback chain. It knows nothing about the
// database or the CLI.
package embed

import (
	"context"
	"fmt"
	"strings"
)

type contextKey string

const forceLocalKey contextKey = "force-local"

// WithForceLocal marks a context so the chain restricts itself to local
// providers (used when indexing sensitive files under a cloud model).
func WithForceLocal(ctx context.Context, force bool) context.Context {
	return context.WithValue(ctx, forceLocalKey, force)
}

func isForceLocal(ctx context.Context) bool {
	val, _ := ctx.Value(forceLocalKey).(bool)
	return val
}

// ModelInfo holds embedding model metadata.
type ModelInfo struct {
	Name string
	Dims int
}

// Embedder generates embeddings for a given model.
type Embedder interface {
	ModelInfo(ctx context.Context, model string) (*ModelInfo, error)
	Embed(ctx context.Context, model string, inputs ...string) ([][]float32, error)
	EmbedSingle(ctx context.Context, model, text string) ([]float32, error)
	ListModels(ctx context.Context) ([]string, error)
}

// ProviderInstance is one provider in the chain, flagged local or remote.
type ProviderInstance struct {
	Name     string
	Embedder Embedder
	Local    bool
}

// ChainEmbedder tries providers in preference order, skipping remote ones when
// privacy mode is on or the context forces local.
type ChainEmbedder struct {
	providers []ProviderInstance
	privacy   bool
}

// NewChainEmbedder builds a chain over the given ordered providers.
func NewChainEmbedder(providers []ProviderInstance, privacy bool) *ChainEmbedder {
	return &ChainEmbedder{providers: providers, privacy: privacy}
}

// SetPrivacy toggles privacy mode (skip remote providers).
func (ce *ChainEmbedder) SetPrivacy(privacy bool) {
	ce.privacy = privacy
}

func (ce *ChainEmbedder) skip(p ProviderInstance, forceLocal bool) bool {
	return (ce.privacy || forceLocal) && !p.Local
}

func (ce *ChainEmbedder) Embed(ctx context.Context, model string, inputs ...string) ([][]float32, error) {
	forceLocal := isForceLocal(ctx)
	var lastErr error
	for _, p := range ce.providers {
		if ce.skip(p, forceLocal) {
			continue
		}
		res, err := p.Embedder.Embed(ctx, model, inputs...)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("chain: falha ao gerar embeddings: %w", lastErr)
}

func (ce *ChainEmbedder) EmbedSingle(ctx context.Context, model, text string) ([]float32, error) {
	forceLocal := isForceLocal(ctx)
	var lastErr error
	for _, p := range ce.providers {
		if ce.skip(p, forceLocal) {
			continue
		}
		res, err := p.Embedder.EmbedSingle(ctx, model, text)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("chain: falha ao gerar embedding single: %w", lastErr)
}

func (ce *ChainEmbedder) ModelInfo(ctx context.Context, model string) (*ModelInfo, error) {
	forceLocal := isForceLocal(ctx)
	var lastErr error
	for _, p := range ce.providers {
		if ce.skip(p, forceLocal) {
			continue
		}
		res, err := p.Embedder.ModelInfo(ctx, model)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("chain: falha ao obter model info: %w", lastErr)
}

func (ce *ChainEmbedder) ListModels(ctx context.Context) ([]string, error) {
	forceLocal := isForceLocal(ctx)
	var models []string
	seen := make(map[string]bool)
	for _, p := range ce.providers {
		if ce.skip(p, forceLocal) {
			continue
		}
		list, err := p.Embedder.ListModels(ctx)
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
		return nil, fmt.Errorf("chain: nenhum modelo de embedding disponível")
	}
	return models, nil
}

// InferDims returns the embedding dimension for a known model name, or 0 when
// unknown (callers then rely on a provider's ModelInfo or a DB probe).
func InferDims(model string) int {
	model = strings.ToLower(model)
	switch {
	case strings.Contains(model, "nomic"):
		return 768
	case strings.Contains(model, "bge-m3"), strings.Contains(model, "mxbai"), strings.Contains(model, "qwen3"):
		return 1024
	case strings.Contains(model, "gemini-embedding-2"), strings.Contains(model, "text-embedding-3-large"):
		return 3072
	default:
		return 0
	}
}
