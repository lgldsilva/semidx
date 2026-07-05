package embed

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCircuitBreakerAllowOnFirstCall(t *testing.T) {
	cb := newCircuitBreaker(3, time.Minute)
	if !cb.allow() {
		t.Error("expected allow on fresh circuit breaker")
	}
}

func TestCircuitBreakerSuccessResets(t *testing.T) {
	cb := newCircuitBreaker(3, time.Minute)

	cb.recordFailure()
	cb.recordFailure()
	cb.recordSuccess() // resets to 0

	if !cb.allow() {
		t.Error("expected allow after success resets failures")
	}
}

func TestCircuitBreakerBlocksAfterThreshold(t *testing.T) {
	cb := newCircuitBreaker(3, time.Minute)

	cb.recordFailure()
	cb.recordFailure()
	cb.recordFailure()

	if cb.allow() {
		t.Error("expected block after 3 consecutive failures")
	}
}

func TestCircuitBreakerHalfOpenAfterCooldown(t *testing.T) {
	cb := newCircuitBreaker(3, 1*time.Millisecond)

	cb.recordFailure()
	cb.recordFailure()
	cb.recordFailure()

	if cb.allow() {
		t.Fatal("expected immediate block after 3 failures")
	}

	// Wait for cooldown to expire.
	time.Sleep(5 * time.Millisecond)

	if !cb.allow() {
		t.Fatal("expected allow after cooldown (half-open)")
	}
}

func TestCircuitBreakerReopensAfterFailedProbe(t *testing.T) {
	cb := newCircuitBreaker(3, 50*time.Millisecond)

	// Trip the circuit.
	for i := 0; i < 3; i++ {
		cb.recordFailure()
	}

	// Wait for cooldown.
	time.Sleep(80 * time.Millisecond)

	// Half-open probe: allow succeeds.
	if !cb.allow() {
		t.Fatal("expected half-open probe to be allowed")
	}
	// Probe fails -> circuit reopens.
	cb.recordFailure()

	// Should be blocked again (failures = 4 >= 3, openUntil reset).
	if cb.allow() {
		t.Error("expected circuit to reopen after failed probe")
	}
}

func TestCircuitBreakerClosesAfterSuccessfulProbe(t *testing.T) {
	cb := newCircuitBreaker(3, 50*time.Millisecond)

	// Trip the circuit.
	for i := 0; i < 3; i++ {
		cb.recordFailure()
	}

	// Wait for cooldown.
	time.Sleep(80 * time.Millisecond)

	// Half-open probe succeeds.
	if !cb.allow() {
		t.Fatal("expected half-open probe to be allowed")
	}
	cb.recordSuccess()

	// Circuit should be closed now — allow succeeds.
	if !cb.allow() {
		t.Error("expected circuit to close after successful probe")
	}
}

func TestCircuitBreakerUnderThreshold(t *testing.T) {
	cb := newCircuitBreaker(3, time.Minute)

	cb.recordFailure()
	cb.recordFailure()

	// 2 failures < 3 threshold -> still allowed.
	if !cb.allow() {
		t.Error("expected allow with 2 failures (under threshold)")
	}
}

func TestCircuitBreakerDefaultThreshold(t *testing.T) {
	cb := newCircuitBreaker(0, 0)
	if cb.threshold != defaultFailureThreshold {
		t.Errorf("threshold = %d, want %d", cb.threshold, defaultFailureThreshold)
	}
	if cb.cooldown != defaultCooldown {
		t.Errorf("cooldown = %v, want %v", cb.cooldown, defaultCooldown)
	}
}

// --- circuitEmbedder integration tests ---------------------------------------

// errEmbedder always fails on every method.
type errEmbedder struct{}

func (e *errEmbedder) ModelInfo(_ context.Context, model string) (*ModelInfo, error) {
	return nil, errors.New("always fails")
}
func (e *errEmbedder) Embed(_ context.Context, _ string, _ ...string) ([][]float32, error) {
	return nil, errors.New("always fails")
}
func (e *errEmbedder) EmbedSingle(_ context.Context, _, _ string) ([]float32, error) {
	return nil, errors.New("always fails")
}
func (e *errEmbedder) ListModels(_ context.Context) ([]string, error) {
	return nil, errors.New("always fails")
}

func TestCircuitEmbedderOpensOnFailure(t *testing.T) {
	// Use a short cooldown to avoid waiting too long if the test fails.
	ce := wrapWithCircuit("test", &errEmbedder{}, 2, time.Minute)

	// First call: fails, failure count = 1.
	_, err := ce.Embed(context.Background(), "m", "x")
	if err == nil {
		t.Fatal("expected error from errEmbedder")
	}

	// Second call: fails, failure count = 2 >= threshold -> circuit opens.
	_, err = ce.Embed(context.Background(), "m", "x")
	if err == nil {
		t.Fatal("expected error from errEmbedder")
	}

	// Third call: circuit breaker open.
	_, err = ce.Embed(context.Background(), "m", "x")
	if err == nil {
		t.Fatal("expected circuit breaker open error")
	}
	if !strings.Contains(err.Error(), "circuit breaker open") {
		t.Errorf("error = %q, want 'circuit breaker open'", err)
	}
}

// okEmbedder always succeeds.
type okEmbedder struct {
	name string
}

func (e *okEmbedder) ModelInfo(_ context.Context, model string) (*ModelInfo, error) {
	return &ModelInfo{Name: model, Dims: 3}, nil
}
func (e *okEmbedder) Embed(_ context.Context, _ string, inputs ...string) ([][]float32, error) {
	out := make([][]float32, len(inputs))
	for i := range inputs {
		out[i] = []float32{1}
	}
	return out, nil
}
func (e *okEmbedder) EmbedSingle(_ context.Context, _, _ string) ([]float32, error) {
	return []float32{1}, nil
}
func (e *okEmbedder) ListModels(_ context.Context) ([]string, error) {
	return []string{e.name + "-model"}, nil
}

func TestCircuitEmbedderClosesOnSuccess(t *testing.T) {
	ce := wrapWithCircuit("test", &okEmbedder{name: "good"}, 3, time.Minute)

	// Succeed multiple times — circuit stays closed.
	for i := 0; i < 5; i++ {
		vec, err := ce.EmbedSingle(context.Background(), "m", "hello")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if len(vec) != 1 || vec[0] != 1 {
			t.Fatalf("call %d: unexpected vector %v", i, vec)
		}
	}

	// ModelInfo and ListModels also work.
	info, err := ce.ModelInfo(context.Background(), "m")
	if err != nil || info.Dims != 3 {
		t.Fatalf("ModelInfo = %+v, err %v", info, err)
	}
	models, err := ce.ListModels(context.Background())
	if err != nil || len(models) != 1 {
		t.Fatalf("ListModels = %v, err %v", models, err)
	}
}

func TestCircuitEmbedderModelInfoRespectsBreaker(t *testing.T) {
	ce := wrapWithCircuit("test", &errEmbedder{}, 1, time.Minute)

	// First failure opens the circuit.
	_, err := ce.ModelInfo(context.Background(), "m")
	if err == nil || strings.Contains(err.Error(), "circuit breaker open") {
		t.Fatal("expected inner error, not circuit breaker")
	}

	// Circuit is open now.
	_, err = ce.ModelInfo(context.Background(), "m")
	if err == nil {
		t.Fatal("expected circuit breaker open error")
	}
	if !strings.Contains(err.Error(), "circuit breaker open") {
		t.Errorf("error = %q, want circuit breaker open", err)
	}
}

func TestCircuitEmbedderListModelsRespectsBreaker(t *testing.T) {
	ce := wrapWithCircuit("test", &errEmbedder{}, 1, time.Minute)

	_, err := ce.ListModels(context.Background())
	if err == nil || strings.Contains(err.Error(), "circuit breaker open") {
		t.Fatal("expected inner error, not circuit breaker")
	}

	_, err = ce.ListModels(context.Background())
	if err == nil {
		t.Fatal("expected circuit breaker open error")
	}
}

func TestCircuitEmbedderRecoversAfterCooldown(t *testing.T) {
	ce := wrapWithCircuit("test", &errEmbedder{}, 2, 30*time.Millisecond)

	// Trip the circuit.
	_, _ = ce.Embed(context.Background(), "m", "x")
	_, err := ce.Embed(context.Background(), "m", "x")
	if err == nil {
		t.Fatal("expected error")
	}

	// Circuit is open — should get circuit breaker error.
	_, err = ce.Embed(context.Background(), "m", "x")
	if !strings.Contains(err.Error(), "circuit breaker open") {
		t.Fatalf("expected circuit breaker open, got %v", err)
	}

	// Wait for cooldown then replace inner with a working embedder.
	time.Sleep(60 * time.Millisecond)

	// Replace inner so the probe succeeds.
	if cew, ok := ce.(*circuitEmbedder); ok {
		cew.inner = &okEmbedder{}
	}

	// Half-open probe should succeed now.
	vec, err := ce.EmbedSingle(context.Background(), "m", "hello")
	if err != nil {
		t.Fatalf("expected recovery after cooldown, got %v", err)
	}
	if len(vec) == 0 {
		t.Fatal("expected embedding vector")
	}
}
