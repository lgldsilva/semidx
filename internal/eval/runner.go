package eval

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

// Observation is the runtime-independent result of one retrieval query.
// Keeping this adapter small lets benchmarks use SQLite, PostgreSQL, fakes, or
// a remote client without coupling the evaluation package to any backend.
type Observation struct {
	Ranked           []Ranked
	Fallback         bool
	Degraded         bool
	Backend          string
	Model            string
	Project          string
	ProjectIdentity  string
	Worktree         string
	IndexFingerprint string
	Dimensions       int
	Duration         time.Duration
}

// SearchFunc executes one labelled query against the system under test.
type SearchFunc func(context.Context, Query) (Observation, error)

// RunnerConfig controls repeatability and semantic validity policy.
type RunnerConfig struct {
	Runs           int
	TopK           int
	Seed           int64
	StrictSemantic bool
	Metadata       RunMetadata
}

// Run executes every query Runs times and averages its metrics. The runner is
// intentionally deterministic: it does not shuffle queries, and Seed is
// recorded in the artifact for future samplers without changing this tier.
func Run(ctx context.Context, ds Dataset, cfg RunnerConfig, search SearchFunc) Results {
	if cfg.Runs <= 0 {
		cfg.Runs = 1
	}
	if cfg.TopK <= 0 {
		cfg.TopK = 10
	}
	metadata := cfg.Metadata
	metadata.Runs = cfg.Runs
	metadata.Seed = cfg.Seed
	metadata.DatasetSHA256, _ = DatasetSHA256(ds)
	out := Results{Version: CurrentDatasetVersion, Metadata: metadata, Total: len(ds.Queries)}
	out.Queries = make([]QueryMetrics, len(ds.Queries))
	metadataSet := hasRuntimeMetadata(metadata)
	var latencies []float64
	for i, q := range ds.Queries {
		qm := QueryMetrics{ID: q.ID, Query: q.Query, Relevant: len(q.Relevant)}
		var ndcg, mrr, precision, recall, found []float64
		queryFallback := false
		for run := 0; run < cfg.Runs; run++ {
			obs, err := search(ctx, q)
			if err != nil {
				qm.Error = err.Error()
				out.Failed++
				break
			}
			qm.Fallback = qm.Fallback || obs.Fallback
			qm.Degraded = qm.Degraded || obs.Degraded
			queryFallback = queryFallback || obs.Fallback || obs.Degraded
			if cfg.StrictSemantic && (obs.Fallback || obs.Degraded) {
				qm.Error = "semantic benchmark received fallback or degraded response"
				out.Failed++
				break
			}
			if !metadataSet {
				applyObservationMetadata(&out.Metadata, obs)
				metadataSet = true
			} else if err := checkObservationMetadata(out.Metadata, obs); err != nil {
				qm.Error = err.Error()
				out.Failed++
				break
			}
			m := Evaluate(q, obs.Ranked, cfg.TopK)
			ndcg = append(ndcg, m.NDCG10)
			mrr = append(mrr, m.MRR)
			precision = append(precision, m.Precision5)
			recall = append(recall, m.Recall10)
			found = append(found, float64(m.Found))
			latencies = append(latencies, float64(obs.Duration)/float64(time.Millisecond))
		}
		if queryFallback {
			out.Fallbacks++
		}
		if qm.Error == "" {
			qm.NDCG10, qm.MRR = median(ndcg), median(mrr)
			qm.Precision5, qm.Recall10 = median(precision), median(recall)
			qm.Found = int(math.Round(median(found)))
		}
		out.Queries[i] = qm
		out.NDCG10 += qm.NDCG10
		out.MRR += qm.MRR
		out.Precision5 += qm.Precision5
		out.Recall10 += qm.Recall10
	}
	completed := out.Total - out.Failed
	if completed > 0 {
		divisor := float64(completed)
		out.NDCG10, out.MRR = out.NDCG10/divisor, out.MRR/divisor
		out.Precision5, out.Recall10 = out.Precision5/divisor, out.Recall10/divisor
	}
	out.LatencyP50MS = percentile(latencies, 0.50)
	out.LatencyP95MS = percentile(latencies, 0.95)
	out.LatencyP99MS = percentile(latencies, 0.99)
	return out
}

func hasRuntimeMetadata(m RunMetadata) bool {
	return m.Backend != "" || m.Model != "" || m.Project != "" || m.ProjectIdentity != "" ||
		m.Worktree != "" || m.IndexFingerprint != "" || m.Dimensions != 0
}

func applyObservationMetadata(m *RunMetadata, obs Observation) {
	m.Backend = obs.Backend
	m.Model = obs.Model
	m.Dimensions = obs.Dimensions
	m.Project = obs.Project
	m.ProjectIdentity = obs.ProjectIdentity
	m.Worktree = obs.Worktree
	m.IndexFingerprint = obs.IndexFingerprint
	if m.Mode == "" {
		m.Mode = "semantic"
	}
}

func checkObservationMetadata(want RunMetadata, got Observation) error {
	checks := []struct {
		name, want, got string
	}{
		{name: "backend", want: want.Backend, got: got.Backend},
		{name: "model", want: want.Model, got: got.Model},
		{name: "project", want: want.Project, got: got.Project},
		{name: "project identity", want: want.ProjectIdentity, got: got.ProjectIdentity},
		{name: "worktree", want: want.Worktree, got: got.Worktree},
		{name: "index fingerprint", want: want.IndexFingerprint, got: got.IndexFingerprint},
	}
	if want.Dimensions != got.Dimensions {
		return fmt.Errorf("benchmark metadata changed for dimensions: %d != %d", want.Dimensions, got.Dimensions)
	}
	for _, check := range checks {
		if check.want != check.got {
			return fmt.Errorf("benchmark metadata changed for %s: %q != %q", check.name, check.want, check.got)
		}
	}
	return nil
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[mid-1] + sorted[mid]) / 2
	}
	return sorted[mid]
}

func percentile(values []float64, fraction float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	index := int(math.Ceil(fraction*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}
