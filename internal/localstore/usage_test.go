package localstore

import (
	"context"
	"testing"
	"time"

	"github.com/lgldsilva/semidx/internal/usage"
)

func TestUsageEventsRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := New(dir + "/index.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.RecordUsageEvent(ctx, usage.Event{
		TS: time.Now().UTC(), Project: "demo", Source: usage.SourceMCP,
		Outcome: usage.OutcomeOK, HitCount: 3, LatencyMS: 12,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordUsageEvent(ctx, usage.Event{
		TS: time.Now().UTC(), Project: "demo", Source: usage.SourceCLI,
		Outcome: usage.OutcomeEmpty, HitCount: 0,
	}); err != nil {
		t.Fatal(err)
	}
	agg, err := s.UsageAggregate(ctx, time.Now().UTC().Add(-time.Hour), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Total != 2 {
		t.Fatalf("total=%d", agg.Total)
	}
	report := usage.BuildReport(agg, usage.DefaultParams(), time.Now().UTC())
	if report.Total != 2 {
		t.Fatalf("report total=%d", report.Total)
	}

	filtered, err := s.UsageAggregate(ctx, time.Now().UTC().Add(-time.Hour), "demo", 0)
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 2 {
		t.Fatalf("filtered=%d", filtered.Total)
	}
	other, err := s.UsageAggregate(ctx, time.Now().UTC().Add(-time.Hour), "missing", 5)
	if err != nil {
		t.Fatal(err)
	}
	if other.Total != 0 {
		t.Fatalf("missing project total=%d", other.Total)
	}
}

func TestUsageFallbackReasonRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := New(dir + "/index.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := s.RecordUsageEvent(ctx, usage.Event{
		TS: now, Project: "demo", Source: usage.SourceMCP,
		Outcome: usage.OutcomeFallback, HitCount: 2, FallbackReason: "ollama: timeout",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordUsageEvent(ctx, usage.Event{
		TS: now, Project: "demo", Source: usage.SourceCLI,
		Outcome: usage.OutcomeOK, HitCount: 1,
	}); err != nil {
		t.Fatal(err)
	}

	agg, err := s.UsageAggregate(ctx, now.Add(-time.Hour), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, c := range agg.ByFallbackReason {
		got[c.Key] = c.Count
	}
	if got["ollama: timeout"] != 1 || got[""] != 1 {
		t.Fatalf("ByFallbackReason = %+v, want ollama: timeout=1 and empty=1", agg.ByFallbackReason)
	}

	report := usage.BuildReport(agg, usage.DefaultParams(), now)
	if len(report.ByFallbackReason) != 1 || report.ByFallbackReason[0].Key != "ollama: timeout" {
		t.Fatalf("report ByFallbackReason = %+v", report.ByFallbackReason)
	}

	// Existing databases gain the column via the guarded ALTER; a second New on
	// the same file must be idempotent.
	s2, err := New(dir + "/index.db")
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()
}
