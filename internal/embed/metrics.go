package embed

import (
	"context"
	"time"
)

// Observer receives one measurement per embedding call. Implementations must be
// safe for concurrent use. It lets a caller (e.g. the HTTP server) record
// metrics without the embed package depending on any metrics library — CLI and
// tests simply pass a nil Observer and pay nothing.
type Observer interface {
	// ObserveEmbed reports the outcome of one Embed/EmbedSingle call: the model,
	// the number of inputs in the batch, how long it took, and any error.
	ObserveEmbed(model string, inputs int, d time.Duration, err error)
}

// Instrument wraps e so every Embed/EmbedSingle call is timed and reported to
// obs. A nil embedder or nil observer returns e unchanged, so instrumentation
// is strictly opt-in and zero-cost when absent.
func Instrument(e Embedder, obs Observer) Embedder {
	if e == nil || obs == nil {
		return e
	}
	return &instrumented{Embedder: e, obs: obs}
}

// instrumented decorates an Embedder, timing the two embedding calls and
// delegating ModelInfo/ListModels unchanged via the embedded interface.
type instrumented struct {
	Embedder
	obs Observer
}

func (i *instrumented) Embed(ctx context.Context, model string, inputs ...string) ([][]float32, error) {
	start := time.Now()
	out, err := i.Embedder.Embed(ctx, model, inputs...)
	i.obs.ObserveEmbed(model, len(inputs), time.Since(start), err)
	return out, err
}

func (i *instrumented) EmbedSingle(ctx context.Context, model, text string) ([]float32, error) {
	start := time.Now()
	out, err := i.Embedder.EmbedSingle(ctx, model, text)
	i.obs.ObserveEmbed(model, 1, time.Since(start), err)
	return out, err
}
