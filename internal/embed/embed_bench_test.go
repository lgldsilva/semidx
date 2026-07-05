package embed

import (
	"context"
	"testing"
)

// BenchmarkEmbedBatch measures the chain embedder dispatch overhead without real providers.
func BenchmarkEmbedBatch_EmptyChain(b *testing.B) {
	chain := NewChainEmbedder(nil, false)
	ctx := context.Background()
	inputs := make([]string, 100)
	for i := range inputs {
		inputs[i] = "benchmark input chunk content for embedding"
	}
	b.ResetTimer()
	for b.Loop() {
		_, _ = chain.Embed(ctx, "test", inputs...)
	}
}
