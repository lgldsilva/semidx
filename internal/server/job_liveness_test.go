package server

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type countingRequeuer struct{ calls atomic.Int64 }

func (c *countingRequeuer) RequeueStaleJobs(context.Context, time.Duration) (int64, error) {
	c.calls.Add(1)
	return 0, nil
}

// Regression: the stale-job sweep only ran at startup, so a job orphaned by a
// worker crash after boot stayed "running" forever.
func TestSweepStaleJobsRunsPeriodically(t *testing.T) {
	t.Parallel()
	srv := New(&fakeStore{}, fakeEmbedder{}, nil)
	r := &countingRequeuer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		srv.sweepStaleJobs(ctx, r, 5*time.Millisecond)
		close(done)
	}()
	deadline := time.After(5 * time.Second)
	for r.calls.Load() < 2 {
		select {
		case <-deadline:
			t.Fatalf("sweep ran %d times, want periodic runs", r.calls.Load())
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sweep loop did not stop on context cancel")
	}
}
