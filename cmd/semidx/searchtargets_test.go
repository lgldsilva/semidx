package main

import (
	"testing"

	"github.com/lgldsilva/semidx/internal/clientconfig"
)

func TestRemoteSearchProjectRef(t *testing.T) {
	cfg := &clientconfig.Config{DefaultProject: "published-sdk"}
	if got := remoteSearchProjectRef("", cfg); got != "published-sdk" {
		t.Fatalf("remoteSearchProjectRef(empty) = %q, want published-sdk", got)
	}
	if got := remoteSearchProjectRef(".", cfg); got != "." {
		t.Fatalf("remoteSearchProjectRef(dot) = %q, want explicit dot preserved", got)
	}
	if got := remoteSearchProjectRef("other", cfg); got != "other" {
		t.Fatalf("remoteSearchProjectRef(explicit) = %q, want other", got)
	}
	if got := remoteSearchProjectRef("", nil); got != "" {
		t.Fatalf("remoteSearchProjectRef(nil) = %q, want empty", got)
	}
}
