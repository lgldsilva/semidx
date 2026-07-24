package store

import (
	"context"
	"testing"
	"time"

	"github.com/lgldsilva/semidx/internal/usage"
)

func TestUsageEventsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := s.RecordUsageEvent(ctx, usage.Event{
		Project: "alpha", Source: usage.SourceMCP, Outcome: usage.OutcomeOK,
		HitCount: 2, LatencyMS: 11, Keyword: false, Graph: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordUsageEvent(ctx, usage.Event{
		TS: now, Project: "alpha", Source: usage.SourceCLI, Outcome: usage.OutcomeEmpty,
		HitCount: 0, QueryHash: "abc", QueryText: "should-store-when-set",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordUsageEvent(ctx, usage.Event{
		TS: now, Project: "beta", Source: usage.Source("weird"), Outcome: "",
		HitCount: 1,
	}); err != nil {
		t.Fatal(err)
	}

	agg, err := s.UsageAggregate(ctx, now.Add(-time.Hour), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Total != 3 {
		t.Fatalf("total=%d", agg.Total)
	}
	if len(agg.ByProject) == 0 || len(agg.BySource) == 0 || len(agg.ByOutcome) == 0 {
		t.Fatalf("incomplete aggregate: %+v", agg)
	}

	filtered, err := s.UsageAggregate(ctx, now.Add(-time.Hour), "alpha", 0)
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 2 {
		t.Fatalf("filtered total=%d", filtered.Total)
	}

	if _, err := s.usageGroup(ctx, now.Add(-time.Hour), "", "not-a-col", 0); err == nil {
		t.Fatal("expected invalid column error")
	}
}
