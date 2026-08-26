package main

import (
	"testing"

	"github.com/lgldsilva/semidx/internal/clientconfig"
)

func TestRemoteSearchDefaultFallback(t *testing.T) {
	cfg := &clientconfig.Config{DefaultProject: "published-sdk"}
	if got := remoteSearchDefaultFallback("", cfg); got != "published-sdk" {
		t.Fatalf("empty = %q, want published-sdk", got)
	}
	if got := remoteSearchDefaultFallback(".", cfg); got != "" {
		t.Fatalf("dot fallback = %q, want empty so cwd failure stays visible", got)
	}
	if got := remoteSearchDefaultFallback("other", cfg); got != "" {
		t.Fatalf("explicit fallback = %q, want empty", got)
	}
	if got := remoteSearchDefaultFallback("", nil); got != "" {
		t.Fatalf("nil cfg = %q, want empty", got)
	}
}
