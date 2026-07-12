package embed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// TestInferDimsCaseInsensitiveProperty proves InferDims is case-insensitive for
// ANY input (kills a mutation dropping the strings.ToLower normalization) and
// only ever returns one of the known dimension buckets.
func TestInferDimsCaseInsensitiveProperty(t *testing.T) {
	valid := map[int]bool{0: true, 768: true, 1024: true, 3072: true}
	rapid.Check(t, func(rt *rapid.T) {
		s := rapid.String().Draw(rt, "s")
		lower := InferDims(strings.ToLower(s))
		upper := InferDims(strings.ToUpper(s))
		mixed := InferDims(s)
		if lower != upper || lower != mixed {
			rt.Fatalf("InferDims not case-insensitive for %q: lower=%d upper=%d mixed=%d", s, lower, upper, mixed)
		}
		if !valid[mixed] {
			rt.Fatalf("InferDims(%q) = %d, not a known bucket", s, mixed)
		}
	})
}

// TestChainFallbackOrderProperty: for any pattern of provider failures (all
// providers local, so none are skipped), the chain tries providers strictly in
// order, stops at the first success, and returns that provider's vector. If all
// fail it errors after trying every provider. This exhaustively exercises the
// fallback loop's control flow.
func TestChainFallbackOrderProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 6).Draw(rt, "n")
		fails := make([]bool, n)
		for i := range fails {
			fails[i] = rapid.Bool().Draw(rt, "fail")
		}

		var calls []string
		providers := make([]ProviderInstance, n)
		for i := 0; i < n; i++ {
			providers[i] = ProviderInstance{
				Name:  string(rune('a' + i)),
				Local: true, // never skipped
				Embedder: &fakeEmbedder{
					name:   string(rune('a' + i)),
					fail:   fails[i],
					single: []float32{float32(i)},
					calls:  &calls,
				},
			}
		}
		chain := NewChainEmbedder(providers, false)

		firstOK := -1
		for i, f := range fails {
			if !f {
				firstOK = i
				break
			}
		}

		res, err := chain.Embed(context.Background(), "m", "x")
		if firstOK < 0 {
			if err == nil {
				rt.Fatalf("all providers failed but Embed returned no error")
			}
			if len(calls) != n {
				rt.Fatalf("all-fail tried %d providers, want %d", len(calls), n)
			}
			return
		}
		if err != nil {
			rt.Fatalf("Embed errored though provider %d succeeds: %v", firstOK, err)
		}
		if len(res) != 1 || len(res[0]) != 1 || res[0][0] != float32(firstOK) {
			rt.Fatalf("Embed returned %v, want vector from provider %d", res, firstOK)
		}
		if len(calls) != firstOK+1 {
			rt.Fatalf("tried %d providers, want %d (stop at first success)", len(calls), firstOK+1)
		}
	})
}

// TestOpenAIRespectsContextCancellation proves an already-cancelled context is
// honoured before any network round trip completes.
func TestOpenAIRespectsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"embedding":[1,2,3]}]}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := NewOpenAIClient(srv.URL, "k")
	if _, err := c.Embed(ctx, "m", "x"); err == nil {
		t.Error("Embed should fail under a cancelled context")
	}
}

func TestOllamaRespectsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"embeddings":[[1,2,3]]}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := NewOllamaClient(srv.URL)
	if _, err := c.Embed(ctx, "m", "x"); err == nil {
		t.Error("Embed should fail under a cancelled context")
	}
	if _, err := c.ModelInfo(ctx, "m"); err == nil {
		t.Error("ModelInfo should fail under a cancelled context")
	}
	if _, err := c.ListModels(ctx); err == nil {
		t.Error("ListModels should fail under a cancelled context")
	}
}
