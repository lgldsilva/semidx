package embed

import (
	"testing"
	"time"
)

// TestCircuitHalfOpenSingleProbe covers the half-open branch: once the cooldown
// expires, only one probe is admitted; concurrent callers are held back until
// the probe resolves.
func TestCircuitHalfOpenSingleProbe(t *testing.T) {
	cb := newCircuitBreaker(1, 20*time.Millisecond)
	cb.recordFailure() // failures >= threshold -> open

	time.Sleep(40 * time.Millisecond) // cooldown elapses -> half-open

	if ok, _ := cb.allow(); !ok {
		t.Fatal("half-open must admit the first probe")
	}
	if ok, _ := cb.allow(); ok {
		t.Fatal("half-open must reject a second probe while one is in flight")
	}

	cb.recordSuccess() // probe succeeded -> closed
	if ok, _ := cb.allow(); !ok {
		t.Fatal("circuit must be closed after a successful probe")
	}

	// A failing probe re-opens the circuit.
	cb.recordFailure()
	cb.recordFailure()
	if ok, _ := cb.allow(); ok {
		t.Fatal("circuit must be open again after the probe fails")
	}
}
