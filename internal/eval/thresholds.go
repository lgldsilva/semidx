package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const CurrentThresholdVersion = 1

// ComparisonThresholds defines the maximum candidate-minus-baseline regression
// accepted by bench compare. Nil fields are not enforced; explicit zero values
// reject any regression for that metric.
type ComparisonThresholds struct {
	Version                 int      `json:"version"`
	MaxNDCG10Drop           *float64 `json:"max_ndcg_at_10_drop,omitempty"`
	MaxMRRDrop              *float64 `json:"max_mrr_drop,omitempty"`
	MaxPrecision5Drop       *float64 `json:"max_precision_at_5_drop,omitempty"`
	MaxRecall10Drop         *float64 `json:"max_recall_at_10_drop,omitempty"`
	MaxFailedIncrease       *int     `json:"max_failed_queries_increase,omitempty"`
	MaxFallbackIncrease     *int     `json:"max_fallback_queries_increase,omitempty"`
	MaxLatencyP95IncreaseMS *float64 `json:"max_latency_p95_increase_ms,omitempty"`
}

// LoadComparisonThresholds reads and validates a strict versioned threshold
// file selected explicitly by the CLI user.
func LoadComparisonThresholds(path string) (ComparisonThresholds, error) {
	// #nosec G304 -- path is an explicit CLI-selected threshold artifact.
	f, err := os.Open(path)
	if err != nil {
		return ComparisonThresholds{}, fmt.Errorf("open comparison thresholds: %w", err)
	}
	defer func() { _ = f.Close() }()
	var thresholds ComparisonThresholds
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&thresholds); err != nil {
		return ComparisonThresholds{}, fmt.Errorf("decode comparison thresholds: %w", err)
	}
	if err := ensureJSONEOF(dec); err != nil {
		return ComparisonThresholds{}, err
	}
	if err := validateComparisonThresholds(thresholds); err != nil {
		return ComparisonThresholds{}, err
	}
	return thresholds, nil
}

func validateComparisonThresholds(thresholds ComparisonThresholds) error {
	if thresholds.Version != CurrentThresholdVersion {
		return fmt.Errorf("unsupported comparison threshold version %d", thresholds.Version)
	}
	for name, value := range map[string]*float64{
		"max_ndcg_at_10_drop":         thresholds.MaxNDCG10Drop,
		"max_mrr_drop":                thresholds.MaxMRRDrop,
		"max_precision_at_5_drop":     thresholds.MaxPrecision5Drop,
		"max_recall_at_10_drop":       thresholds.MaxRecall10Drop,
		"max_latency_p95_increase_ms": thresholds.MaxLatencyP95IncreaseMS,
	} {
		if value != nil && *value < 0 {
			return fmt.Errorf("%s must be non-negative", name)
		}
	}
	for name, value := range map[string]*int{
		"max_failed_queries_increase":   thresholds.MaxFailedIncrease,
		"max_fallback_queries_increase": thresholds.MaxFallbackIncrease,
	} {
		if value != nil && *value < 0 {
			return fmt.Errorf("%s must be non-negative", name)
		}
	}
	return nil
}

// EvaluateComparisonThresholds rejects every configured regression exceeded by
// a candidate-minus-baseline Results delta.
func EvaluateComparisonThresholds(delta Results, thresholds ComparisonThresholds) error {
	var failures []string
	checkDrop := func(name string, got float64, maxDrop *float64) {
		if maxDrop != nil && got < -*maxDrop {
			failures = append(failures, fmt.Sprintf("%s dropped %.6f (maximum %.6f)", name, -got, *maxDrop))
		}
	}
	checkDrop("nDCG@10", delta.NDCG10, thresholds.MaxNDCG10Drop)
	checkDrop("MRR", delta.MRR, thresholds.MaxMRRDrop)
	checkDrop("Precision@5", delta.Precision5, thresholds.MaxPrecision5Drop)
	checkDrop("Recall@10", delta.Recall10, thresholds.MaxRecall10Drop)
	if thresholds.MaxFailedIncrease != nil && delta.Failed > *thresholds.MaxFailedIncrease {
		failures = append(failures, fmt.Sprintf("failed queries increased by %d (maximum %d)", delta.Failed, *thresholds.MaxFailedIncrease))
	}
	if thresholds.MaxFallbackIncrease != nil && delta.Fallbacks > *thresholds.MaxFallbackIncrease {
		failures = append(failures, fmt.Sprintf("fallback queries increased by %d (maximum %d)", delta.Fallbacks, *thresholds.MaxFallbackIncrease))
	}
	if thresholds.MaxLatencyP95IncreaseMS != nil && delta.LatencyP95MS > *thresholds.MaxLatencyP95IncreaseMS {
		failures = append(failures, fmt.Sprintf("latency p95 increased %.3f ms (maximum %.3f ms)", delta.LatencyP95MS, *thresholds.MaxLatencyP95IncreaseMS))
	}
	if len(failures) > 0 {
		return fmt.Errorf("comparison thresholds failed: %s", strings.Join(failures, "; "))
	}
	return nil
}
