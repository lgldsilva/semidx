package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvaluateComparisonThresholds(t *testing.T) {
	maxDrop := 0.01
	maxFailed := 0
	maxLatency := 25.0
	thresholds := ComparisonThresholds{
		Version:                 CurrentThresholdVersion,
		MaxNDCG10Drop:           &maxDrop,
		MaxFailedIncrease:       &maxFailed,
		MaxLatencyP95IncreaseMS: &maxLatency,
	}
	if err := EvaluateComparisonThresholds(Results{
		NDCG10: -0.005, Failed: 0, LatencyP95MS: 20,
	}, thresholds); err != nil {
		t.Fatalf("acceptable delta rejected: %v", err)
	}
	err := EvaluateComparisonThresholds(Results{
		NDCG10: -0.02, Failed: 1, LatencyP95MS: 30,
	}, thresholds)
	if err == nil {
		t.Fatal("regressions above thresholds should fail")
	}
	for _, want := range []string{"nDCG@10", "failed queries", "latency p95"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("threshold error %q does not mention %q", err, want)
		}
	}
}

func TestLoadComparisonThresholdsValidatesSchema(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.json")
	if err := os.WriteFile(valid, []byte(`{"version":1,"max_mrr_drop":0.02}`), 0o600); err != nil {
		t.Fatal(err)
	}
	thresholds, err := LoadComparisonThresholds(valid)
	if err != nil || thresholds.MaxMRRDrop == nil || *thresholds.MaxMRRDrop != 0.02 {
		t.Fatalf("LoadComparisonThresholds = %+v, %v", thresholds, err)
	}
	for name, content := range map[string]string{
		"malformed": `{"version":`,
		"future":    `{"version":99}`,
		"negative":  `{"version":1,"max_recall_at_10_drop":-0.1}`,
		"trailing":  `{"version":1} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name+".json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadComparisonThresholds(path); err == nil {
				t.Fatalf("invalid thresholds %s unexpectedly accepted", name)
			}
		})
	}
}

func TestComparisonGateDetectsRankingMutation(t *testing.T) {
	maxDrop := 0.0
	thresholds := ComparisonThresholds{
		Version: CurrentThresholdVersion, MaxNDCG10Drop: &maxDrop,
	}
	baseline := Results{NDCG10: 1}
	mutated := Results{NDCG10: 0.4}
	delta := Results{NDCG10: mutated.NDCG10 - baseline.NDCG10}
	if err := EvaluateComparisonThresholds(delta, thresholds); err == nil {
		t.Fatal("ranking mutation should fail a zero-regression gate")
	}
}
