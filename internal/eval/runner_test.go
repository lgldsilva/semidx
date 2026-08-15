package eval

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunRepeatsAndAveragesDeterministically(t *testing.T) {
	ds := Dataset{Version: 2, Queries: []Query{{ID: "q", Query: "auth", Relevant: []Relevance{{File: "auth.go", Grade: 3}}}}}
	calls := 0
	results := Run(context.Background(), ds, RunnerConfig{Runs: 3, TopK: 10, Seed: 42}, func(context.Context, Query) (Observation, error) {
		calls++
		return Observation{
			Ranked: []Ranked{{File: "auth.go"}}, Route: "vector", Backend: "sqlite", Model: "bge-m3",
			Project: "app", ProjectIdentity: "git:app", Worktree: "/work/app", IndexFingerprint: "idx",
		}, nil
	})
	if calls != 3 || results.Failed != 0 || results.NDCG10 != 1 || results.Metadata.Seed != 42 {
		t.Fatalf("calls=%d results=%+v", calls, results)
	}
	if results.Metadata.DatasetSHA256 == "" || results.Metadata.ProjectIdentity != "git:app" || results.Metadata.Worktree != "/work/app" {
		t.Fatalf("missing comparison metadata: %+v", results.Metadata)
	}
	if results.Queries[0].Route != "vector" || results.RouteCounts["vector"] != 1 {
		t.Fatalf("route evidence = query=%q counts=%v, want vector/1", results.Queries[0].Route, results.RouteCounts)
	}
}

func TestRunMarksMixedObservedRoutes(t *testing.T) {
	ds := Dataset{Version: 2, Queries: []Query{{ID: "q", Query: "auth", Relevant: []Relevance{{File: "auth.go", Grade: 3}}}}}
	call := 0
	results := Run(context.Background(), ds, RunnerConfig{Runs: 2}, func(context.Context, Query) (Observation, error) {
		call++
		route := "hybrid"
		if call == 2 {
			route = "vector"
		}
		return Observation{Ranked: []Ranked{{File: "auth.go"}}, Route: route}, nil
	})
	if results.Queries[0].Route != "mixed" || results.RouteCounts["mixed"] != 1 {
		t.Fatalf("mixed route evidence = query=%q counts=%v", results.Queries[0].Route, results.RouteCounts)
	}
}

func TestRunRecordsRuntimeAndLatencyDistribution(t *testing.T) {
	ds := Dataset{Version: 2, Queries: []Query{{ID: "q", Query: "auth", Relevant: []Relevance{{File: "auth.go", Grade: 3}}}}}
	call := 0
	results := Run(context.Background(), ds, RunnerConfig{Runs: 3}, func(context.Context, Query) (Observation, error) {
		call++
		return Observation{
			Ranked: []Ranked{{File: "auth.go"}}, Backend: "sqlite", Model: "bge-m3",
			Dimensions: 1024, Project: "app", ProjectIdentity: "git:app",
			Worktree: "/work/app", IndexFingerprint: "idx",
			Duration: time.Duration([]int{10, 30, 20}[call-1]) * time.Millisecond,
		}, nil
	})
	if results.Metadata.Dimensions != 1024 || results.LatencyP50MS != 20 ||
		results.LatencyP95MS != 30 || results.LatencyP99MS != 30 {
		t.Fatalf("runtime metadata/latency distribution = %+v", results)
	}
}

func TestRunUsesMedianAcrossRepeatedRankings(t *testing.T) {
	ds := Dataset{Version: 2, Queries: []Query{{ID: "q", Query: "auth", Relevant: []Relevance{{File: "auth.go", Grade: 3}}}}}
	rankings := [][]Ranked{
		{{File: "auth.go"}},
		{{File: "x.go"}, {File: "y.go"}, {File: "auth.go"}},
		{{File: "x.go"}},
	}
	call := 0
	results := Run(context.Background(), ds, RunnerConfig{Runs: len(rankings)}, func(context.Context, Query) (Observation, error) {
		ranked := rankings[call]
		call++
		return Observation{Ranked: ranked}, nil
	})
	if got, want := results.MRR, 1.0/3.0; got != want {
		t.Fatalf("MRR median = %v, want %v", got, want)
	}
}

func TestRunStrictSemanticRejectsFallback(t *testing.T) {
	ds := Dataset{Version: 2, Queries: []Query{{ID: "q", Query: "auth", Relevant: []Relevance{{File: "auth.go", Grade: 3}}}}}
	results := Run(context.Background(), ds, RunnerConfig{StrictSemantic: true}, func(context.Context, Query) (Observation, error) {
		return Observation{Fallback: true}, nil
	})
	if results.Failed != 1 || results.Queries[0].Error == "" || results.Fallbacks != 1 {
		t.Fatalf("strict results=%+v", results)
	}
}

func TestRunRecordsSearchErrors(t *testing.T) {
	ds := Dataset{Version: 2, Queries: []Query{{ID: "q", Query: "auth", Relevant: []Relevance{{File: "auth.go", Grade: 3}}}}}
	results := Run(context.Background(), ds, RunnerConfig{}, func(context.Context, Query) (Observation, error) {
		return Observation{}, errors.New("provider unavailable")
	})
	if results.Failed != 1 || results.Queries[0].Error != "provider unavailable" {
		t.Fatalf("error results=%+v", results)
	}
}

func TestRunCountsFallbackQueriesNotRepeatedObservations(t *testing.T) {
	ds := Dataset{Version: 2, Queries: []Query{{ID: "q", Query: "auth", Relevant: []Relevance{{File: "auth.go", Grade: 3}}}}}
	results := Run(context.Background(), ds, RunnerConfig{Runs: 3}, func(context.Context, Query) (Observation, error) {
		return Observation{Fallback: true}, nil
	})
	if results.Fallbacks != 1 {
		t.Fatalf("fallback_queries = %d, want 1 query regardless of run count", results.Fallbacks)
	}
}

func TestRunRejectsMetadataDriftBetweenObservations(t *testing.T) {
	ds := Dataset{Version: 2, Queries: []Query{{ID: "q", Query: "auth", Relevant: []Relevance{{File: "auth.go", Grade: 3}}}}}
	call := 0
	results := Run(context.Background(), ds, RunnerConfig{Runs: 2}, func(context.Context, Query) (Observation, error) {
		call++
		return Observation{
			Ranked:  []Ranked{{File: "auth.go"}},
			Backend: "sqlite", Project: "app", IndexFingerprint: []string{"idx-a", "idx-b"}[call-1],
		}, nil
	})
	if results.Failed != 1 || results.Queries[0].Error == "" {
		t.Fatalf("metadata drift should fail the query: %+v", results)
	}
}
