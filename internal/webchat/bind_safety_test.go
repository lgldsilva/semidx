package webchat

import (
	"log/slog"
	"testing"
)

func TestIsLoopbackListen(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8976":    true,
		"localhost:8976":    true,
		"[::1]:8976":        true,
		":8976":             false, // all interfaces
		"0.0.0.0:8976":      false,
		"192.168.1.10:8976": false,
	}
	for addr, want := range cases {
		if got := isLoopbackListen(addr); got != want {
			t.Errorf("isLoopbackListen(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestCheckBindSafety(t *testing.T) {
	s := &Server{listenAddr: "127.0.0.1:8976"}
	if err := s.checkBindSafety(); err != nil {
		t.Fatalf("loopback bind should be allowed: %v", err)
	}

	pub := &Server{listenAddr: "0.0.0.0:8976", log: slog.Default()}
	if err := pub.checkBindSafety(); err == nil {
		t.Fatal("non-loopback bind without override must be refused")
	}

	t.Setenv(allowPublicEnv, "1")
	if err := pub.checkBindSafety(); err != nil {
		t.Fatalf("override should allow non-loopback bind: %v", err)
	}
}
