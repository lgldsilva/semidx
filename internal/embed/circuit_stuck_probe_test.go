package embed

import (
	"context"
	"testing"
	"time"
)

// A half-open probe that ends without a verdict (caller cancelled, or a
// deterministic UnknownModelError) used to leave probing=true forever, so the
// provider was refused until process restart.
func TestCircuitProbeReleasedOnCancel(t *testing.T) {
	calls := []string{}
	ce := &circuitEmbedder{inner: &fakeEmbedder{name: "p", fail: true, calls: &calls}, cb: newCircuitBreaker(1, 10*time.Millisecond), name: "p"}
	ce.cb.recordFailure() // open
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = ce.Embed(ctx, "m", "x") // probe ends with ctx error: no verdict

	time.Sleep(20 * time.Millisecond)
	if ok, _ := ce.cb.allow(); !ok {
		t.Fatal("breaker stuck: probe slot never released after a cancelled probe")
	}
}

func TestCircuitProbeReleasedOnUnknownModel(t *testing.T) {
	ce := &circuitEmbedder{inner: &unknownModelEmbedder{}, cb: newCircuitBreaker(1, 10*time.Millisecond), name: "p"}
	ce.cb.recordFailure()
	time.Sleep(20 * time.Millisecond)

	_, _ = ce.ModelInfo(context.Background(), "nope")

	time.Sleep(20 * time.Millisecond)
	if ok, _ := ce.cb.allow(); !ok {
		t.Fatal("breaker stuck: probe slot never released after UnknownModelError")
	}
}
