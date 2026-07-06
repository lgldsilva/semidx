package embed

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const (
	defaultFailureThreshold = 3                // consecutive failures before opening circuit
	defaultCooldown         = 30 * time.Second // how long the circuit stays open
	circuitOpenFmt          = "circuit breaker open for %s"
)

// RetryableError wraps an error with a retry-after duration for transient
// failures where retrying after the cooldown is appropriate.
type RetryableError struct {
	Err   error
	After time.Duration
}

func (e *RetryableError) Error() string             { return e.Err.Error() }
func (e *RetryableError) Unwrap() error             { return e.Err }
func (e *RetryableError) RetryAfter() time.Duration { return e.After }

// circuitBreaker tracks a single provider's failure state.
type circuitBreaker struct {
	mu        sync.Mutex
	failures  int
	openUntil time.Time
	threshold int
	cooldown  time.Duration
}

func newCircuitBreaker(threshold int, cooldown time.Duration) *circuitBreaker {
	if threshold <= 0 {
		threshold = defaultFailureThreshold
	}
	if cooldown <= 0 {
		cooldown = defaultCooldown
	}
	return &circuitBreaker{
		threshold: threshold,
		cooldown:  cooldown,
	}
}

// allow checks whether the circuit is closed (allow call). When the circuit
// is open (cooldown period), returns false and the remaining cooldown duration.
// After the cooldown expires the circuit enters a half-open state and returns
// true once (probing).
func (cb *circuitBreaker) allow() (bool, time.Duration) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if remaining := time.Until(cb.openUntil); remaining > 0 {
		return false, remaining
	}
	// Half-open: allow one probe, reset timer.
	if cb.failures >= cb.threshold {
		cb.openUntil = time.Time{}
	}
	return true, 0
}

func (cb *circuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures = 0
	cb.openUntil = time.Time{}
}

func (cb *circuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures++
	if cb.failures >= cb.threshold {
		cb.openUntil = time.Now().Add(cb.cooldown)
	}
}

// circuitEmbedder wraps an Embedder with a circuit breaker. When the circuit
// is open the Embedder is skipped entirely until the cooldown expires.
type circuitEmbedder struct {
	inner Embedder
	cb    *circuitBreaker
	name  string
}

func (ce *circuitEmbedder) Embed(ctx context.Context, model string, inputs ...string) ([][]float32, error) {
	if ok, remaining := ce.cb.allow(); !ok {
		return nil, &RetryableError{
			Err:   fmt.Errorf(circuitOpenFmt, ce.name),
			After: remaining,
		}
	}
	result, err := ce.inner.Embed(ctx, model, inputs...)
	if err != nil {
		ce.cb.recordFailure()
		return nil, err
	}
	ce.cb.recordSuccess()
	return result, nil
}

func (ce *circuitEmbedder) EmbedSingle(ctx context.Context, model, text string) ([]float32, error) {
	if ok, remaining := ce.cb.allow(); !ok {
		return nil, &RetryableError{
			Err:   fmt.Errorf(circuitOpenFmt, ce.name),
			After: remaining,
		}
	}
	result, err := ce.inner.EmbedSingle(ctx, model, text)
	if err != nil {
		ce.cb.recordFailure()
		return nil, err
	}
	ce.cb.recordSuccess()
	return result, nil
}

func (ce *circuitEmbedder) ModelInfo(ctx context.Context, model string) (*ModelInfo, error) {
	if ok, remaining := ce.cb.allow(); !ok {
		return nil, &RetryableError{
			Err:   fmt.Errorf(circuitOpenFmt, ce.name),
			After: remaining,
		}
	}
	result, err := ce.inner.ModelInfo(ctx, model)
	if err != nil {
		ce.cb.recordFailure()
		return nil, err
	}
	ce.cb.recordSuccess()
	return result, nil
}

func (ce *circuitEmbedder) ListModels(ctx context.Context) ([]string, error) {
	if ok, remaining := ce.cb.allow(); !ok {
		return nil, &RetryableError{
			Err:   fmt.Errorf(circuitOpenFmt, ce.name),
			After: remaining,
		}
	}
	result, err := ce.inner.ListModels(ctx)
	if err != nil {
		ce.cb.recordFailure()
		return nil, err
	}
	ce.cb.recordSuccess()
	return result, nil
}

// wrapWithCircuit decorates an Embedder with a circuit breaker. When threshold
// or cooldown are zero the defaults (3 failures, 30 s) are used.
func wrapWithCircuit(name string, inner Embedder, threshold int, cooldown time.Duration) Embedder {
	return &circuitEmbedder{
		inner: inner,
		name:  name,
		cb:    newCircuitBreaker(threshold, cooldown),
	}
}

var _ Embedder = (*circuitEmbedder)(nil)
