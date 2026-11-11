package server

import (
	"context"
	"time"
)

// staleJobSweepInterval is how often the running server re-checks for jobs
// orphaned by a dead worker (not only at startup).
const staleJobSweepInterval = 5 * time.Minute

// jobHeartbeatInterval is how often a worker refreshes its running job's
// heartbeat; it must stay well below staleJobAge.
const jobHeartbeatInterval = time.Minute

// staleJobRequeuer is the optional store capability behind the stale sweep.
type staleJobRequeuer interface {
	RequeueStaleJobs(context.Context, time.Duration) (int64, error)
}

// jobToucher is the optional store capability behind job heartbeats.
type jobToucher interface {
	TouchJob(ctx context.Context, id int) error
}

// requeueStaleJobs runs one stale-job sweep and records what it requeued.
func (s *Server) requeueStaleJobs(ctx context.Context, r staleJobRequeuer) {
	count, err := r.RequeueStaleJobs(ctx, staleJobAge)
	if err != nil {
		s.log.Warn("requeue stale jobs failed", "err", err)
		return
	}
	if count > 0 {
		s.jobsQueued.Add(float64(count))
		s.log.Warn("requeued stale jobs", "count", count)
	}
}

// sweepStaleJobs repeats the stale-job sweep every interval until ctx ends,
// so a job orphaned by a worker that died after startup is eventually retried.
func (s *Server) sweepStaleJobs(ctx context.Context, r staleJobRequeuer, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.requeueStaleJobs(ctx, r)
		}
	}
}

// startJobHeartbeat refreshes the job's heartbeat until the returned stop
// function is called, keeping a long-running job out of the stale sweep.
func (s *Server) startJobHeartbeat(ctx context.Context, jobID int) (stop func()) {
	t, ok := s.store.(jobToucher)
	if !ok {
		return func() {}
	}
	hbCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(jobHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				if err := t.TouchJob(hbCtx, jobID); err != nil && hbCtx.Err() == nil {
					s.log.Warn("job heartbeat", "job", jobID, "err", err)
				}
			}
		}
	}()
	return func() { cancel(); <-done }
}
