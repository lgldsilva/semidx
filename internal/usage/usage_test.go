package usage

import (
	"testing"
	"time"
)

func TestParseSource(t *testing.T) {
	t.Parallel()
	if ParseSource("MCP") != SourceMCP {
		t.Fatal("expected mcp")
	}
	if ParseSource("nope") != SourceUnknown {
		t.Fatal("expected unknown")
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

func TestHashQuery(t *testing.T) {
	t.Parallel()
	if HashQuery("") != "" {
		t.Fatal("empty")
	}
	a := HashQuery("token validation")
	b := HashQuery("token validation")
	if a != b || len(a) != 64 {
		t.Fatalf("hash=%q", a)
	}
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
