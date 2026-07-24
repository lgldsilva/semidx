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
