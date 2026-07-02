package main

import (
	"context"
	"fmt"
)

type contextKey string

const forceLocalKey contextKey = "force-local"

// WithForceLocal insere a flag de forçar provedor local no contexto.
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

// Embedder defines the interface for text embedding models.
type Embedder interface {
	ModelInfo(ctx context.Context, model string) (*ModelInfo, error)
	Embed(ctx context.Context, model string, inputs ...string) ([][]float32, error)
	EmbedSingle(ctx context.Context, model, text string) ([]float32, error)
	ListModels(ctx context.Context) ([]string, error)
}

// ProviderInstance holds one embedding provider.
type ProviderInstance struct {
	Name     string
	Embedder Embedder
	Local    bool
}

// ChainEmbedder walks a list of providers in preference order.
type ChainEmbedder struct {
	providers []ProviderInstance
	privacy   bool
}

func NewChainEmbedder(providers []ProviderInstance, privacy bool) *ChainEmbedder {
	return &ChainEmbedder{providers: providers, privacy: privacy}
}

func (ce *ChainEmbedder) SetPrivacy(privacy bool) {
	ce.privacy = privacy
}

func (ce *ChainEmbedder) Embed(ctx context.Context, model string, inputs ...string) ([][]float32, error) {
	forceLocal := isForceLocal(ctx)
	var lastErr error
	for _, p := range ce.providers {
		if (ce.privacy || forceLocal) && !p.Local {
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
		if (ce.privacy || forceLocal) && !p.Local {
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
		if (ce.privacy || forceLocal) && !p.Local {
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
		if (ce.privacy || forceLocal) && !p.Local {
			continue
		}
		list, err := p.Embedder.ListModels(ctx)
		if err == nil {
			for _, m := range list {
				if !seen[m] {
					seen[m] = true
					models = append(models, m)
				}
			}
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("chain: nenhum modelo de embedding disponível")
	}
	return models, nil
}
