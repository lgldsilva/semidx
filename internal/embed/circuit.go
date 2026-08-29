package embed

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// isTransient returns true for errors that may resolve with a quick retry:
// network timeouts, connection resets, HTTP 429 (rate limit), HTTP 502/503/504.
// Context cancellation is not considered a provider transient failure.
func isTransient(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "429") ||
		strings.Contains(msg, "502") ||
		strings.Contains(msg, "503") ||
		strings.Contains(msg, "504") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "service unavailable") ||
		strings.Contains(msg, "bad gateway") ||
		strings.Contains(msg, "gateway timeout") ||
		strings.Contains(msg, "temporarily unavailable")
}

// circuitBreaker tracks a single provider's failure state.
type circuitBreaker struct {
	mu        sync.Mutex
	failures  int
	openUntil time.Time
	probing   bool // a half-open probe is in flight
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
	// Half-open: let a single probe through and hold everyone else back until it
	// resolves (recordSuccess closes the circuit; recordFailure re-opens it).
	if cb.failures >= cb.threshold {
		if cb.probing {
			return false, cb.cooldown
		}
		cb.probing = true
		cb.openUntil = time.Time{}
	}
	return true, 0
}

func (cb *circuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures = 0
	cb.openUntil = time.Time{}
	cb.probing = false
}

func (cb *circuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures++
	cb.probing = false
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

// quickRetryBackoffs is the delay before each extra attempt on a transient
// provider error (429/5xx/timeout). The first call has no delay; two extra
// attempts gives three tries total before the circuit counts a failure.
var quickRetryBackoffs = []time.Duration{50 * time.Millisecond, 150 * time.Millisecond}

func waitOrCancel(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// retryTransient runs fn immediately, then retries on isTransient errors with
// quickRetryBackoffs. Context cancellation aborts without further attempts.
func retryTransient(ctx context.Context, fn func() error) error {
	err := fn()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !isTransient(err) {
		return err
	}
	for _, wait := range quickRetryBackoffs {
		if werr := waitOrCancel(ctx, wait); werr != nil {
			return werr
		}
		err = fn()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !isTransient(err) {
			return err
		}
	}
	return err
}

func (ce *circuitEmbedder) Embed(ctx context.Context, model string, inputs ...string) ([][]float32, error) {
	if ok, remaining := ce.cb.allow(); !ok {
		return nil, &RetryableError{
			Err:   fmt.Errorf(circuitOpenFmt, ce.name),
			After: remaining,
		}
	}
	var result [][]float32
	err := retryTransient(ctx, func() error {
		var e error
		result, e = ce.inner.Embed(ctx, model, inputs...)
		return e
	})
	if err == nil {
		ce.cb.recordSuccess()
		return result, nil
	}
	if ctx.Err() == nil {
		ce.cb.recordFailure()
	}
	return nil, err
}

func (ce *circuitEmbedder) EmbedSingle(ctx context.Context, model, text string) ([]float32, error) {
	if ok, remaining := ce.cb.allow(); !ok {
		return nil, &RetryableError{
			Err:   fmt.Errorf(circuitOpenFmt, ce.name),
			After: remaining,
		}
	}
	var result []float32
	err := retryTransient(ctx, func() error {
		var e error
		result, e = ce.inner.EmbedSingle(ctx, model, text)
		return e
	})
	if err == nil {
		ce.cb.recordSuccess()
		return result, nil
	}
	if ctx.Err() == nil {
		ce.cb.recordFailure()
	}
	return nil, err
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
		// A dims-catalog miss (UnknownModelError) is deterministic and says
		// nothing about provider health: counting it would open this provider's
		// breaker for every other model/project whenever an unknown model is
		// looked up repeatedly. Only real provider failures feed the breaker.
		var ume *UnknownModelError
		if !errors.As(err, &ume) && ctx.Err() == nil {
			ce.cb.recordFailure()
		}
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
		if ctx.Err() == nil {
			ce.cb.recordFailure()
		}
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
