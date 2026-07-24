package usage

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestParseSource(t *testing.T) {
	t.Parallel()
	cases := map[string]Source{
		"MCP":     SourceMCP,
		" cli ":   SourceCLI,
		"ADMIN":   SourceAdmin,
		"sdk":     SourceSDK,
		"nope":    SourceUnknown,
		"":        SourceUnknown,
		"unknown": SourceUnknown,
	}
	for in, want := range cases {
		if got := ParseSource(in); got != want {
			t.Fatalf("ParseSource(%q)=%q want %q", in, got, want)
		}
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()
	if Classify(0, false, false) != OutcomeEmpty {
		t.Fatal("empty")
	}
	if Classify(3, true, false) != OutcomeFallback {
		t.Fatal("fallback")
	}
	if Classify(2, false, true) != OutcomeFallback {
		t.Fatal("keyword-only")
	}
	if Classify(1, false, false) != OutcomeOK {
		t.Fatal("ok")
	}
}

func TestBuildReportFindings(t *testing.T) {
	t.Parallel()
	agg := Aggregate{
		Total: 10,
		ByOutcome: []Count{
			{Key: string(OutcomeEmpty), Count: 6},
			{Key: string(OutcomeOK), Count: 4},
		},
		BySource: []Count{
			{Key: string(SourceUnknown), Count: 10},
		},
		ByProject: []Count{{Key: "a", Count: 10}},
	}
	r := BuildReport(agg, DefaultParams(), time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	if r.Total != 10 {
		t.Fatalf("total=%d", r.Total)
	}
	kinds := map[string]bool{}
	for _, f := range r.Findings {
		kinds[f.Kind] = true
	}
	if !kinds["high_empty_rate"] {
		t.Fatalf("expected high_empty_rate, got %#v", r.Findings)
	}
	if !kinds["unknown_sources_only"] {
		t.Fatalf("expected unknown_sources_only")
	}
	if len(r.BlindSpots) == 0 {
		t.Fatal("expected blind spots")
	}
	text := FormatText(r)
	if !containsAll(text, "# semidx usage", "high_empty_rate", "Blind spots") {
		t.Fatalf("bad text:\n%s", text)
	}
}

func TestBuildReportDefaultsAndBranches(t *testing.T) {
	t.Parallel()
	// Zero params + zero time → defaults; empty aggregate → no_searches.
	r := BuildReport(Aggregate{}, Params{}, time.Time{})
	if r.SinceDays != 30 || r.GeneratedAt == "" {
		t.Fatalf("defaults: %+v", r)
	}
	if len(r.Findings) != 1 || r.Findings[0].Kind != "no_searches" {
		t.Fatalf("findings=%#v", r.Findings)
	}
	if !contains(FormatText(r), "None.") {
		t.Fatal("expected empty count sections")
	}

	// Truncation + fallback/error findings + project filter in FormatText.
	projects := make([]Count, 0, 12)
	for i := 0; i < 12; i++ {
		projects = append(projects, Count{Key: string(rune('a' + i)), Count: 12 - i})
	}
	agg := Aggregate{
		Total:     20,
		ByProject: projects,
		BySource: []Count{
			{Key: string(SourceMCP), Count: 8},
			{Key: string(SourceCLI), Count: 12},
		},
		ByOutcome: []Count{
			{Key: string(OutcomeFallback), Count: 10},
			{Key: string(OutcomeError), Count: 4},
			{Key: string(OutcomeOK), Count: 6},
		},
	}
	r = BuildReport(agg, Params{SinceDays: -1, TopLimit: 3, Project: "demo"}, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	if len(r.ByProject) != 3 {
		t.Fatalf("top limit: %d", len(r.ByProject))
	}
	kinds := map[string]bool{}
	for _, f := range r.Findings {
		kinds[f.Kind] = true
	}
	if !kinds["high_fallback_rate"] || !kinds["elevated_error_rate"] {
		t.Fatalf("findings=%#v", r.Findings)
	}
	text := FormatText(r)
	if !containsAll(text, "Project filter: demo", "high_fallback_rate", "elevated_error_rate") {
		t.Fatalf("bad text:\n%s", text)
	}
}

func TestHashQuery(t *testing.T) {
	t.Parallel()
	if HashQuery("") != "" {
		t.Fatal("empty")
	}
	if HashQuery("  ") != "" {
		t.Fatal("whitespace")
	}
	a := HashQuery("token validation")
	b := HashQuery("token validation")
	if a != b || len(a) != 64 {
		t.Fatalf("hash=%q", a)
	}
}

func TestWithSourceAndSourceFrom(t *testing.T) {
	t.Parallel()
	if SourceFrom(context.Background()) != SourceUnknown {
		t.Fatal("empty ctx")
	}
	ctx := WithSource(context.Background(), SourceMCP)
	if SourceFrom(ctx) != SourceMCP {
		t.Fatal("mcp")
	}
	ctx = WithSource(context.Background(), SourceCLI)
	if SourceFrom(ctx) != SourceCLI {
		t.Fatal("cli")
	}
}

func TestNopAndStoreRecorder(t *testing.T) {
	t.Parallel()
	Nop{}.Record(context.Background(), Event{})

	var (
		mu   sync.Mutex
		got  []Event
		nils int
	)
	w := storeWriterFunc(func(_ context.Context, e Event) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, e)
		return nil
	})

	var nilRec *StoreRecorder
	nilRec.Record(context.Background(), Event{QueryText: "x"})

	r := &StoreRecorder{Store: nil}
	r.Record(context.Background(), Event{})

	r = &StoreRecorder{Store: w, LogQueries: false}
	r.Record(context.Background(), Event{
		Project: "p", Source: Source("MCP"), Outcome: OutcomeOK,
		QueryText: "secret query",
	})
	r.LogQueries = true
	r.Record(context.Background(), Event{
		TS:      time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		Project: "p", Source: SourceCLI, Outcome: OutcomeEmpty,
		QueryText: "keep me", QueryHash: "",
	})

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("got %d events (nils=%d)", len(got), nils)
	}
	if got[0].QueryText != "" || got[0].QueryHash == "" || got[0].Source != SourceMCP {
		t.Fatalf("redacted event: %+v", got[0])
	}
	if got[0].TS.IsZero() {
		t.Fatal("expected TS fill")
	}
	if got[1].QueryText != "keep me" || got[1].QueryHash == "" {
		t.Fatalf("logged event: %+v", got[1])
	}
}

type storeWriterFunc func(context.Context, Event) error

func (f storeWriterFunc) RecordUsageEvent(ctx context.Context, e Event) error {
	return f(ctx, e)
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !contains(s, p) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > 0 && stringIndex(s, sub) >= 0))
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
