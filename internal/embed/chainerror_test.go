package embed

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// failEmbedder always fails with a fixed error.
type failEmbedder struct{ err error }

func (e failEmbedder) ModelInfo(context.Context, string) (*ModelInfo, error) { return nil, e.err }
func (e failEmbedder) Embed(context.Context, string, ...string) ([][]float32, error) {
	return nil, e.err
}
func (e failEmbedder) EmbedSingle(context.Context, string, string) ([]float32, error) {
	return nil, e.err
}
func (e failEmbedder) ListModels(context.Context) ([]string, error) { return nil, e.err }

func TestChainErrorCollectsProviderFailures(t *testing.T) {
	chain := NewChainEmbedder([]ProviderInstance{
		{Name: "gemini", Embedder: failEmbedder{err: errors.New("HTTP 503 service unavailable")}},
		{Name: "ollama", Local: true, Embedder: failEmbedder{err: errors.New("request timeout")}},
	}, false)

	_, err := chain.EmbedSingle(context.Background(), "m", "hi")
	var ce *ChainError
	if !errors.As(err, &ce) {
		t.Fatalf("error type = %T, want *ChainError", err)
	}
	if len(ce.Failures) != 2 ||
		ce.Failures[0].Name != "gemini" || ce.Failures[1].Name != "ollama" {
		t.Fatalf("failures = %+v, want [gemini ollama]", ce.Failures)
	}
	// The message keeps the historical "chain: op: lastErr" shape.
	if got, want := err.Error(), "chain: failed to generate embedding: request timeout"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestChainErrorPreservesRetryableUnwrap(t *testing.T) {
	re := &RetryableError{Err: errors.New("circuit breaker open for ollama"), After: 3 * time.Second}
	chain := NewChainEmbedder([]ProviderInstance{
		{Name: "ollama", Local: true, Embedder: failEmbedder{err: re}},
	}, false)

	_, err := chain.EmbedSingle(context.Background(), "m", "hi")
	var got *RetryableError
	if !errors.As(err, &got) {
		t.Fatalf("errors.As(RetryableError) failed for %v", err)
	}
	if got.RetryAfter() != 3*time.Second {
		t.Errorf("RetryAfter = %v, want 3s", got.RetryAfter())
	}
}

func TestChainErrorNoProvidersKeepsPlainError(t *testing.T) {
	chain := NewChainEmbedder([]ProviderInstance{
		{Name: "remote", Embedder: failEmbedder{err: errors.New("boom")}},
	}, true) // privacy on: the only provider is skipped

	_, err := chain.EmbedSingle(context.Background(), "m", "hi")
	var ce *ChainError
	if errors.As(err, &ce) {
		t.Fatalf("no-provider failure should not be a ChainError: %v", err)
	}
}

func TestSummarizeFailure(t *testing.T) {
	chain := func(name string, err error) error {
		return &ChainError{Op: "failed to generate embedding", Failures: []ProviderFailure{{Name: name, Err: err}}}
	}
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"timeout", chain("ollama", errors.New("request timeout")), "ollama: timeout"},
		{"deadline exceeded", chain("ollama", fmt.Errorf("call: %w", context.DeadlineExceeded)), "ollama: timeout"},
		{"5xx", chain("gemini", errors.New("HTTP 503")), "gemini: 5xx"},
		{"rate limited", chain("openrouter", errors.New("429 too many requests")), "openrouter: rate limited"},
		{"circuit open", chain("ollama", &RetryableError{Err: errors.New("circuit breaker open for ollama"), After: time.Second}), "ollama: circuit open"},
		{"unreachable", chain("ollama", errors.New("dial tcp: connection refused")), "ollama: unreachable"},
		{"generic", chain("gemini", errors.New("weird failure")), "gemini: error"},
		{"bare error has no provider", errors.New("service unavailable"), "5xx"},
		{"wrapped chain unwraps", fmt.Errorf("parallel: %w", chain("ollama", errors.New("timeout"))), "ollama: timeout"},
		{"last failure wins", &ChainError{Op: "op", Failures: []ProviderFailure{
			{Name: "gemini", Err: errors.New("HTTP 503")},
			{Name: "ollama", Err: errors.New("timeout")},
		}}, "ollama: timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SummarizeFailure(tc.err); got != tc.want {
				t.Errorf("SummarizeFailure(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}
