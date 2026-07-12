package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryPing(t *testing.T) {
	fast := []time.Duration{time.Millisecond, time.Millisecond} // 3 attempts

	t.Run("succeeds on first attempt", func(t *testing.T) {
		calls := 0
		err := retryPing(context.Background(), func(context.Context) error {
			calls++
			return nil
		}, fast)
		if err != nil {
			t.Fatalf("err = %v; want nil", err)
		}
		if calls != 1 {
			t.Fatalf("ping called %d times; want 1 (no retry on success)", calls)
		}
	})

	t.Run("retries then succeeds", func(t *testing.T) {
		calls := 0
		err := retryPing(context.Background(), func(context.Context) error {
			calls++
			if calls < 3 {
				return errors.New("db not ready")
			}
			return nil
		}, fast)
		if err != nil {
			t.Fatalf("err = %v; want nil", err)
		}
		if calls != 3 {
			t.Fatalf("ping called %d times; want 3", calls)
		}
	})

	t.Run("returns the last error when every attempt fails", func(t *testing.T) {
		calls := 0
		sentinel := errors.New("still down")
		err := retryPing(context.Background(), func(context.Context) error {
			calls++
			return sentinel
		}, fast)
		if !errors.Is(err, sentinel) {
			t.Fatalf("err = %v; want %v", err, sentinel)
		}
		if want := len(fast) + 1; calls != want {
			t.Fatalf("ping called %d times; want %d (len(delays)+1)", calls, want)
		}
	})

	t.Run("honours context cancellation during backoff", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		// A long delay guarantees we are parked in the wait when the context is
		// cancelled, so the select must return ctx.Err() rather than sleep it out.
		slow := []time.Duration{time.Hour}
		go func() {
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()
		err := retryPing(ctx, func(context.Context) error {
			return errors.New("down")
		}, slow)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v; want context.Canceled", err)
		}
	})
}
