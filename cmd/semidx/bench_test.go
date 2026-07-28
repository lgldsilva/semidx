package main

import (
	"context"
	"strings"
	"testing"
)

func TestBenchCommandExposesVersionedEvaluationSubcommands(t *testing.T) {
	cmd := newBenchCmd(&deps{})
	for _, name := range []string{"retrieval", "compare", "validate-dataset"} {
		found, _, err := cmd.Find([]string{name})
		if err != nil || found == cmd || found.Name() != name {
			t.Fatalf("bench subcommand %q not registered: found=%v err=%v", name, found, err)
		}
	}
	compare, _, err := cmd.Find([]string{"compare"})
	if err != nil {
		t.Fatal(err)
	}
	if compare.Flags().Lookup("fail-if") == nil {
		t.Error("compare flag --fail-if is missing")
	}
	retrieval, _, err := cmd.Find([]string{"retrieval"})
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"dataset", "output", "mode", "runs", "seed", "strict-semantic"} {
		if retrieval.Flags().Lookup(flag) == nil {
			t.Errorf("retrieval flag --%s is missing", flag)
		}
	}
}

func TestValidateRetrievalMode(t *testing.T) {
	for _, tc := range []struct {
		input     string
		wantMode  string
		wantGraph bool
		wantErr   bool
	}{
		{input: "keyword", wantMode: "keyword"},
		{input: "vector", wantMode: "vector"},
		{input: "hybrid", wantMode: "hybrid"},
		{input: "HYBRID-GRAPH", wantMode: "hybrid-graph", wantGraph: true},
		{input: "unknown", wantErr: true},
	} {
		mode, graph, err := validateRetrievalMode(tc.input)
		if (err != nil) != tc.wantErr || mode != tc.wantMode || graph != tc.wantGraph {
			t.Errorf("validateRetrievalMode(%q) = %q, %v, %v", tc.input, mode, graph, err)
		}
	}
}

func TestRemoteBenchmarkRejectsVectorOnlyBeforeNetworkAccess(t *testing.T) {
	_, err := (&deps{}).runRemoteSearch(context.Background(), searchCall{vectorOnly: true})
	if err == nil || !strings.Contains(err.Error(), "remote API") {
		t.Fatalf("vector-only remote error = %v", err)
	}
}
