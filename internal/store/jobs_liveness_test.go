package store

import (
	"context"
	"testing"
	"time"
)

// Regression: CompleteJob/FailJob updated a job whatever its status, so a
// late worker could overwrite a requeued (queued) or already finished job.
func TestJobTerminalUpdatesRequireRunning(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	pid, err := s.UpsertProject(ctx, "jobs", "/jobs", "m", 3)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	queued, err := s.EnqueueJob(ctx, pid, "full")
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if err := s.CompleteJob(ctx, queued, 1, 1, 0, 0); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}
	if err := s.FailJob(ctx, queued, "late"); err != nil {
		t.Fatalf("FailJob: %v", err)
	}
	if j, _ := s.GetJob(ctx, queued); j == nil || j.Status != "queued" {
		t.Fatalf("queued job after late Complete/Fail = %+v, want still queued", j)
	}

	claimed, err := s.ClaimJob(ctx)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimJob = %+v, %v", claimed, err)
	}
	if err := s.FailJob(ctx, claimed.ID, "boom"); err != nil {
		t.Fatalf("FailJob: %v", err)
	}
	if err := s.CompleteJob(ctx, claimed.ID, 5, 5, 0, 0); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}
	if j, _ := s.GetJob(ctx, claimed.ID); j == nil || j.Status != "failed" || j.Error != "boom" {
		t.Fatalf("failed job after late Complete = %+v, want still failed", j)
	}
}

// The periodic sweep must requeue only jobs whose worker stopped
// heartbeating, never a long job that is still alive.
func TestRequeueStaleJobsHonorsHeartbeat(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	pid, err := s.UpsertProject(ctx, "hb", "/hb", "m", 3)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if _, err := s.EnqueueJob(ctx, pid, "full"); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	j, err := s.ClaimJob(ctx)
	if err != nil || j == nil {
		t.Fatalf("ClaimJob = %+v, %v", j, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE index_jobs SET started_at = NOW() - INTERVAL '2 hours', heartbeat_at = NOW() - INTERVAL '2 hours' WHERE id = $1`, j.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchJob(ctx, j.ID); err != nil {
		t.Fatalf("TouchJob: %v", err)
	}
	if n, err := s.RequeueStaleJobs(ctx, 30*time.Minute); err != nil || n != 0 {
		t.Fatalf("RequeueStaleJobs(live job) = %d, %v; want 0", n, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE index_jobs SET heartbeat_at = NOW() - INTERVAL '1 hour' WHERE id = $1`, j.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.RequeueStaleJobs(ctx, 30*time.Minute); err != nil || n != 1 {
		t.Fatalf("RequeueStaleJobs(dead job) = %d, %v; want 1", n, err)
	}
}
