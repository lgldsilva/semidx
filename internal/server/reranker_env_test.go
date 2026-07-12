package server

import (
	"log/slog"
	"testing"
)

func TestRerankerFromEnv(t *testing.T) {
	log := slog.Default()

	t.Run("unset disables", func(t *testing.T) {
		t.Setenv("SEMIDX_RERANK_WEIGHT", "")
		if rerankerFromEnv(log) != nil {
			t.Error("unset SEMIDX_RERANK_WEIGHT must disable reranking")
		}
	})

	t.Run("valid weight enables", func(t *testing.T) {
		t.Setenv("SEMIDX_RERANK_WEIGHT", "0.4")
		if rerankerFromEnv(log) == nil {
			t.Error("valid weight must enable a reranker")
		}
	})

	for _, bad := range []string{"0", "-0.2", "1.5", "abc"} {
		t.Run("rejects "+bad, func(t *testing.T) {
			t.Setenv("SEMIDX_RERANK_WEIGHT", bad)
			if rerankerFromEnv(log) != nil {
				t.Errorf("weight %q must be rejected (disables reranking)", bad)
			}
		})
	}
}
