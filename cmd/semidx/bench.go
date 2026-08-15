package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/lgldsilva/semidx/internal/eval"
	"github.com/lgldsilva/semidx/internal/gitexec"
	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/indexfingerprint"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/spf13/cobra"
)

type benchQuery = eval.Query

type benchOpts struct {
	project, model, queriesFile string
	topK                        int
	privacy, asJSON             bool
	strictSemantic              bool
	baselineKeyword             bool
}

// benchResults holds per-query and aggregated metrics.
type benchResults struct {
	Model      string         `json:"model"`
	Baseline   string         `json:"baseline,omitempty"` // keyword or empty
	Total      int            `json:"total_queries"`
	Failed     int            `json:"failed_queries"`
	Fallbacks  int            `json:"fallback_queries"`
	NDCG10     float64        `json:"ndcg_at_10"`
	MRR        float64        `json:"mrr"`
	PrecAt5    float64        `json:"precision_at_5"`
	RecallAt10 float64        `json:"recall_at_10"`
	Queries    []queryMetrics `json:"queries"`
}

type queryMetrics struct {
	Query       string  `json:"query"`
	Description string  `json:"description,omitempty"`
	NDCG10      float64 `json:"ndcg_at_10"`
	MRR         float64 `json:"mrr"`
	PrecAt5     float64 `json:"precision_at_5"`
	RecallAt10  float64 `json:"recall_at_10"`
	Found       int     `json:"results_found"`
	Relevant    int     `json:"relevant_total"`
	Fallback    bool    `json:"fallback,omitempty"`
	Degraded    bool    `json:"degraded,omitempty"`
	Error       string  `json:"error,omitempty"`
}

func newBenchCmd(d *deps) *cobra.Command {
	var opts benchOpts
	c := &cobra.Command{
		Use:   "bench",
		Short: "Run search quality benchmarks against ground-truth queries",
		Long: `Benchmark semantic search quality against a set of labelled queries.

A queries file may use the versioned dataset object (version 2) with graded
"relevant" entries, or the legacy JSON array with "relevant_files". Metrics
reported: graded nDCG@10, MRR, Precision@5, Recall@10.

With --baseline-keyword, the benchmark also runs a pure keyword search and
reports the improvement of semantic search over the keyword baseline.`,
		Example: `  semidx bench --project . --queries queries.json
  semidx bench --project myapp --queries queries.json --baseline-keyword --json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			queries, err := loadBenchQueries(opts.queriesFile)
			if err != nil {
				return err
			}
			if len(queries) == 0 {
				return fmt.Errorf("no queries found in %s", opts.queriesFile)
			}

			results := runBenchmarks(cmd, d, opts, queries)

			// Optional: compute keyword baseline and report improvement.
			if opts.baselineKeyword {
				kwResults := runBenchmarksKeyword(cmd, d, opts.project, opts.topK, queries)
				results.Baseline = "keyword"
				if opts.asJSON {
					results.Baseline = fmt.Sprintf("keyword (ndcg:%.3f mrr:%.3f)", kwResults.NDCG10, kwResults.MRR)
				}
			}

			if opts.asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(results); err != nil {
					return err
				}
			} else {
				printBenchResults(results)
			}
			if opts.strictSemantic && results.Failed > 0 {
				return fmt.Errorf("strict semantic benchmark failed: %d query(s) used fallback/degraded mode or errored", results.Failed)
			}
			return nil
		},
	}
	c.Flags().StringVar(&opts.project, "project", ".", "Project path or name")
	c.Flags().StringVar(&opts.queriesFile, "queries", "", "Path to JSON queries file (required)")
	c.Flags().IntVar(&opts.topK, "top-k", 10, "Number of results per query")
	c.Flags().StringVar(&opts.model, "model", "", "Override embedding model")
	c.Flags().BoolVar(&opts.privacy, "privacy", false, "Force local-only providers (Ollama)")
	c.Flags().BoolVar(&opts.strictSemantic, "strict-semantic", false, "Fail benchmark queries that use embedding fallback or degraded mode")
	c.Flags().BoolVar(&opts.asJSON, "json", false, "Output results as JSON")
	c.Flags().BoolVar(&opts.baselineKeyword, "baseline-keyword", false, "Also run keyword search to compare")
	_ = c.MarkFlagRequired("queries")
	c.AddCommand(newBenchRetrievalCmd(d))
	c.AddCommand(newBenchCompareCmd())
	c.AddCommand(newBenchValidateDatasetCmd())
	return c
}

type retrievalBenchOptions struct {
	project        string
	model          string
	dataset        string
	output         string
	mode           string
	topK           int
	runs           int
	seed           int64
	privacy        bool
	asJSON         bool
	strictSemantic bool
}

func newBenchRetrievalCmd(d *deps) *cobra.Command {
	var opts retrievalBenchOptions
	c := &cobra.Command{
		Use:   "retrieval",
		Short: "Run a repeatable, versioned retrieval evaluation",
		RunE: func(cmd *cobra.Command, _ []string) error {
			results, err := runRetrievalEvaluation(cmd, d, opts)
			if err != nil {
				return err
			}
			if opts.output != "" {
				if err := eval.WriteResults(filepath.Clean(opts.output), results); err != nil {
					return err
				}
			}
			if opts.asJSON {
				b, err := eval.MarshalResults(results)
				if err != nil {
					return err
				}
				if _, err := cmd.OutOrStdout().Write(b); err != nil {
					return fmt.Errorf("write benchmark JSON: %w", err)
				}
			} else {
				if err := printEvalResults(cmd, results); err != nil {
					return err
				}
			}
			if results.Failed > 0 {
				return fmt.Errorf("retrieval benchmark failed: %d query(s) invalid or unsuccessful", results.Failed)
			}
			return nil
		},
	}
	c.Flags().StringVar(&opts.project, "project", ".", "Default project path, identity, or unique name")
	c.Flags().StringVar(&opts.model, "model", "", "Override embedding model")
	c.Flags().StringVar(&opts.dataset, "dataset", "", "Path to the versioned dataset (required)")
	c.Flags().StringVar(&opts.output, "output", "", "Write the versioned result artifact to this path")
	c.Flags().StringVar(&opts.mode, "mode", "hybrid", "Retrieval mode: keyword, vector, hybrid, or hybrid-graph")
	c.Flags().IntVar(&opts.topK, "top-k", 10, "Number of ranked results evaluated per query")
	c.Flags().IntVar(&opts.runs, "runs", 1, "Number of repeated executions per query")
	c.Flags().Int64Var(&opts.seed, "seed", 42, "Seed recorded for deterministic samplers")
	c.Flags().BoolVar(&opts.privacy, "privacy", false, "Force local-only embedding providers")
	c.Flags().BoolVar(&opts.asJSON, "json", false, "Write only the result artifact JSON to stdout")
	c.Flags().BoolVar(&opts.strictSemantic, "strict-semantic", false, "Fail on embedding fallback or degraded responses")
	c.Flags().Bool("graph", false, "Internal graph expansion toggle")
	c.Flags().Int("graph-depth", 2, "Internal graph expansion depth")
	_ = c.Flags().MarkHidden("graph")
	_ = c.Flags().MarkHidden("graph-depth")
	_ = c.MarkFlagRequired("dataset")
	return c
}

func newBenchCompareCmd() *cobra.Command {
	var asJSON bool
	var output string
	var failIf string
	c := &cobra.Command{
		Use:   "compare BASELINE.json CANDIDATE.json",
		Short: "Compare compatible retrieval benchmark artifacts",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			baseline, err := eval.LoadResults(filepath.Clean(args[0]))
			if err != nil {
				return err
			}
			candidate, err := eval.LoadResults(filepath.Clean(args[1]))
			if err != nil {
				return err
			}
			delta, err := eval.Compare(baseline, candidate)
			if err != nil {
				return err
			}
			var thresholdErr error
			if failIf != "" {
				thresholds, err := eval.LoadComparisonThresholds(filepath.Clean(failIf))
				if err != nil {
					return err
				}
				thresholdErr = eval.EvaluateComparisonThresholds(delta, thresholds)
			}
			if output != "" {
				if err := eval.WriteResults(filepath.Clean(output), delta); err != nil {
					return err
				}
			}
			if asJSON {
				b, err := eval.MarshalResults(delta)
				if err != nil {
					return err
				}
				_, err = cmd.OutOrStdout().Write(b)
				if err != nil {
					return err
				}
				return thresholdErr
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "nDCG@10 %+.6f  MRR %+.6f  P@5 %+.6f  Recall@10 %+.6f\n",
				delta.NDCG10, delta.MRR, delta.Precision5, delta.Recall10)
			if err != nil {
				return err
			}
			if len(delta.RouteCounts) > 0 {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "route count delta: %s\n", formatRouteCounts(delta.RouteCounts)); err != nil {
					return err
				}
			}
			if len(delta.RouteTransitions) > 0 {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "route transitions: %s\n", formatRouteCounts(delta.RouteTransitions)); err != nil {
					return err
				}
			}
			return thresholdErr
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "Write comparison JSON to stdout")
	c.Flags().StringVar(&output, "output", "", "Write comparison JSON to this path")
	c.Flags().StringVar(&failIf, "fail-if", "", "Fail when candidate deltas exceed thresholds from this JSON file")
	return c
}

func newBenchValidateDatasetCmd() *cobra.Command {
	var dataset string
	var projectRoot string
	var asJSON bool
	c := &cobra.Command{
		Use:   "validate-dataset",
		Short: "Validate a versioned retrieval benchmark dataset",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ds, err := eval.LoadDataset(dataset)
			if err != nil {
				return err
			}
			if projectRoot != "" {
				if err := eval.ValidateFiles(ds, filepath.Clean(projectRoot)); err != nil {
					return err
				}
			}
			hash, err := eval.DatasetSHA256(ds)
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
					"version": ds.Version, "queries": len(ds.Queries), "dataset_sha256": hash,
				})
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "valid dataset: version=%d queries=%d sha256=%s\n", ds.Version, len(ds.Queries), hash)
			return err
		},
	}
	c.Flags().StringVar(&dataset, "dataset", "", "Path to JSON dataset (required)")
	c.Flags().StringVar(&projectRoot, "project", "", "Also verify every judged path under this corpus root")
	c.Flags().BoolVar(&asJSON, "json", false, "Output validation result as JSON")
	_ = c.MarkFlagRequired("dataset")
	return c
}

func runRetrievalEvaluation(cmd *cobra.Command, d *deps, opts retrievalBenchOptions) (eval.Results, error) {
	if opts.runs < 1 {
		return eval.Results{}, fmt.Errorf("--runs must be at least 1")
	}
	if opts.topK < 1 || opts.topK > 1000 {
		return eval.Results{}, fmt.Errorf("--top-k must be between 1 and 1000")
	}
	mode, graph, err := validateRetrievalMode(opts.mode)
	if err != nil {
		return eval.Results{}, err
	}
	if mode == "keyword" && opts.strictSemantic {
		return eval.Results{}, fmt.Errorf("--strict-semantic cannot be combined with --mode keyword")
	}
	ds, err := eval.LoadDataset(filepath.Clean(opts.dataset))
	if err != nil {
		return eval.Results{}, err
	}
	originalKeyword := d.keywordOnly
	originalVector := d.vectorOnly
	d.keywordOnly = mode == "keyword"
	d.vectorOnly = mode == "vector"
	defer func() {
		d.keywordOnly = originalKeyword
		d.vectorOnly = originalVector
	}()

	search := newBenchmarkSearch(cmd, d, opts, graph)
	results := eval.Run(cmd.Context(), ds, eval.RunnerConfig{
		Runs: opts.runs, TopK: opts.topK, Seed: opts.seed, StrictSemantic: opts.strictSemantic,
		Metadata: eval.RunMetadata{
			Commit: benchmarkCommit(cmd.Context()), Mode: mode,
			Environment: runtime.GOOS + "/" + runtime.GOARCH + " " + runtime.Version(),
		},
	}, search)
	return results, nil
}

func validateRetrievalMode(raw string) (mode string, graph bool, err error) {
	switch mode = strings.ToLower(strings.TrimSpace(raw)); mode {
	case "keyword", "vector", "hybrid":
		return mode, false, nil
	case "hybrid-graph":
		return mode, true, nil
	default:
		return "", false, fmt.Errorf("invalid --mode %q (use keyword, vector, hybrid, or hybrid-graph)", raw)
	}
}

type benchmarkProjectMetadata struct {
	backend, identity, worktree, fingerprint string
}

func newBenchmarkSearch(cmd *cobra.Command, d *deps, opts retrievalBenchOptions, graph bool) eval.SearchFunc {
	cache := make(map[string]benchmarkProjectMetadata)
	return func(ctx context.Context, q eval.Query) (eval.Observation, error) {
		started := time.Now()
		projectRef := opts.project
		if strings.TrimSpace(q.ProjectRef) != "" {
			projectRef = q.ProjectRef
		}
		if graph {
			if err := cmd.Flags().Set("graph", "true"); err != nil {
				return eval.Observation{}, err
			}
		}
		results, err := d.runSearchTargets(cmd, projectRef, q.Query, opts.model, opts.topK, opts.privacy)
		if err != nil {
			return eval.Observation{}, err
		}
		if len(results) != 1 {
			return eval.Observation{}, fmt.Errorf("benchmark query %q resolved %d projects; specify an unambiguous project_ref", q.ID, len(results))
		}
		result := results[0]
		if result.resp == nil || result.resp.Project == nil {
			return eval.Observation{}, fmt.Errorf("benchmark query %q returned no resolved project metadata", q.ID)
		}
		cacheKey := result.resp.Project.Identity + "\x00" + result.resp.Project.Name
		meta, ok := cache[cacheKey]
		if !ok {
			meta, err = resolveBenchmarkProjectMetadata(ctx, d, result.resp.Project)
			if err != nil {
				return eval.Observation{}, err
			}
			cache[cacheKey] = meta
		}
		ranked := make([]eval.Ranked, len(result.resp.Results))
		for i, hit := range result.resp.Results {
			ranked[i] = eval.Ranked{File: hit.FilePath}
		}
		return eval.Observation{
			Ranked: ranked, Route: result.resp.Route, Fallback: result.resp.Fallback, Degraded: result.resp.Degraded,
			Backend: meta.backend, Model: result.resp.Model, Project: result.resp.Project.Name,
			ProjectIdentity: meta.identity, Worktree: meta.worktree, IndexFingerprint: meta.fingerprint,
			Dimensions: result.resp.Project.Dims, Duration: time.Since(started),
		}, nil
	}
}

func resolveBenchmarkProjectMetadata(ctx context.Context, d *deps, project *store.Project) (benchmarkProjectMetadata, error) {
	meta := benchmarkProjectMetadata{identity: project.Identity}
	if d.remote() {
		meta.backend = "remote"
		return meta, nil
	}
	if d.localIndexPath != "" {
		meta.backend = "sqlite"
	} else {
		meta.backend = "postgres"
	}
	db, err := d.indexStore(ctx)
	if err != nil {
		return benchmarkProjectMetadata{}, err
	}
	files, err := db.ListFileHashes(ctx, project.ID)
	if err != nil {
		return benchmarkProjectMetadata{}, fmt.Errorf("read benchmark index hashes: %w", err)
	}
	var corpusCommit string
	if strings.TrimSpace(project.Path) != "" {
		info := gitmeta.Resolve(ctx, project.Path)
		if info.IsGit && project.SourceType == "git" {
			meta.worktree = info.Toplevel
			corpusCommit, _ = gitexec.Run(ctx, project.Path, "rev-parse", "HEAD")
		}
	}
	meta.fingerprint = indexfingerprint.ComputeCorpus(meta.identity, corpusCommit, files)
	return meta, nil
}

func benchmarkCommit(ctx context.Context) string {
	if value, err := gitexec.Run(ctx, ".", "rev-parse", "HEAD"); err == nil {
		return value
	}
	return commit
}

func printEvalResults(cmd *cobra.Command, results eval.Results) error {
	w := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(w, "retrieval benchmark: %d queries, %d failed, %d fallback\n", results.Total, results.Failed, results.Fallbacks); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "nDCG@10 %.6f  MRR %.6f  P@5 %.6f  Recall@10 %.6f\n",
		results.NDCG10, results.MRR, results.Precision5, results.Recall10); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "latency p50/p95/p99 %.3f/%.3f/%.3f ms\n",
		results.LatencyP50MS, results.LatencyP95MS, results.LatencyP99MS); err != nil {
		return err
	}
	if len(results.RouteCounts) > 0 {
		if _, err := fmt.Fprintf(w, "observed routes: %s\n", formatRouteCounts(results.RouteCounts)); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "project=%s backend=%s mode=%s fingerprint=%s\n",
		results.Metadata.Project, results.Metadata.Backend, results.Metadata.Mode, results.Metadata.IndexFingerprint)
	return err
}

func formatRouteCounts(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for route := range counts {
		keys = append(keys, route)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, route := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", route, counts[route]))
	}
	return strings.Join(parts, ", ")
}

func loadBenchQueries(path string) ([]benchQuery, error) {
	ds, err := eval.LoadDataset(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	return ds.Queries, nil
}

func runBenchmarks(cmd *cobra.Command, d *deps, opts benchOpts, queries []benchQuery) *benchResults {
	out := &benchResults{Model: opts.model, Total: len(queries)}
	var sumNDCG, sumMRR, sumP5, sumR10 float64
	start := time.Now()

	for i, q := range queries {
		results, err := d.runSearchTargets(cmd, opts.project, q.Query, opts.model, opts.topK, opts.privacy)
		if err != nil {
			out.Failed++
			out.Queries = append(out.Queries, queryMetrics{
				Query: q.Query, Description: q.Description, Relevant: len(q.Relevant), Error: err.Error(),
			})
			continue
		}
		qm, failed := scoreQuery(q, results, opts.topK, opts.strictSemantic)
		if qm.Fallback || qm.Degraded {
			out.Fallbacks++
		}
		if failed {
			out.Failed++
		}
		out.Queries = append(out.Queries, qm)
		if failed {
			continue
		}
		sumNDCG += qm.NDCG10
		sumMRR += qm.MRR
		sumP5 += qm.PrecAt5
		sumR10 += qm.RecallAt10

		// Progress indicator for non-JSON output.
		fmt.Fprintf(os.Stderr, "\r[%d/%d] %s", i+1, len(queries), q.Query)
	}
	fmt.Fprintln(os.Stderr)

	n := float64(out.Total - out.Failed)
	if n > 0 {
		out.NDCG10 = sumNDCG / n
		out.MRR = sumMRR / n
		out.PrecAt5 = sumP5 / n
		out.RecallAt10 = sumR10 / n
	}
	out.Model = fmt.Sprintf("%s (%v)", modelOrDefault(opts.model), time.Since(start).Round(time.Millisecond))
	return out
}

func scoreQuery(q benchQuery, results []projSearch, topK int, strictSemantic bool) (queryMetrics, bool) {
	qm := queryMetrics{Query: q.Query, Description: q.Description, Relevant: len(q.Relevant)}
	for _, ps := range results {
		qm.Fallback = qm.Fallback || ps.resp.Fallback
		qm.Degraded = qm.Degraded || ps.resp.Degraded
	}
	if qm.Fallback || qm.Degraded {
		if strictSemantic {
			qm.Error = "semantic benchmark received fallback or degraded response"
			return qm, true
		}
	}

	var ranked []eval.Ranked
	for _, ps := range results {
		for _, result := range ps.resp.Results {
			ranked = append(ranked, eval.Ranked{File: result.FilePath})
		}
	}
	metrics := eval.Evaluate(q, ranked, topK)
	qm.Found = metrics.Found
	qm.NDCG10 = metrics.NDCG10
	qm.MRR = metrics.MRR
	qm.PrecAt5 = metrics.Precision5
	qm.RecallAt10 = metrics.Recall10
	return qm, false
}

// runBenchmarksKeyword runs pure keyword searches for baseline comparison.
func runBenchmarksKeyword(cmd *cobra.Command, d *deps, project string, topK int, queries []benchQuery) *benchResults {
	// Keyword-only baseline: use the search service with keywordOnly=true.
	// We reuse the existing keyword fallback path by temporarily switching modes.
	origKeyword := d.keywordOnly
	d.keywordOnly = true
	defer func() { d.keywordOnly = origKeyword }()

	out := &benchResults{Total: len(queries)}
	var sumNDCG, sumMRR float64
	for i, q := range queries {
		results, err := d.runSearchTargets(cmd, project, q.Query, "", topK, false)
		qm := queryMetrics{Query: q.Query}
		if err != nil {
			out.Failed++
			continue
		}
		var ranked []eval.Ranked
		for _, ps := range results {
			for _, r := range ps.resp.Results {
				ranked = append(ranked, eval.Ranked{File: r.FilePath})
			}
		}
		metrics := eval.Evaluate(q, ranked, topK)
		qm.Found = metrics.Found
		qm.NDCG10 = metrics.NDCG10
		qm.MRR = metrics.MRR
		sumNDCG += qm.NDCG10
		sumMRR += qm.MRR
		fmt.Fprintf(os.Stderr, "\r[keyword %d/%d] %s", i+1, len(queries), q.Query)
	}
	fmt.Fprintln(os.Stderr)
	n := float64(out.Total - out.Failed)
	if n > 0 {
		out.NDCG10 = sumNDCG / n
		out.MRR = sumMRR / n
	}
	return out
}

// --- output formatting --------------------------------------------------------

func printBenchResults(r *benchResults) {
	fmt.Println()
	fmt.Println(strings.Repeat("═", 60))
	fmt.Printf("  semidx bench results  (%d queries, %d failed, %d fallback)\n", r.Total, r.Failed, r.Fallbacks)
	fmt.Println(strings.Repeat("═", 60))
	fmt.Printf("  nDCG@10:     %.3f\n", r.NDCG10)
	fmt.Printf("  MRR:         %.3f\n", r.MRR)
	fmt.Printf("  Precision@5: %.3f\n", r.PrecAt5)
	fmt.Printf("  Recall@10:   %.3f\n", r.RecallAt10)
	if r.Baseline != "" {
		fmt.Printf("  Baseline:    %s\n", r.Baseline)
	}
	fmt.Printf("  Model:       %s\n", r.Model)
	fmt.Println(strings.Repeat("═", 60))

	// Per-query detail: only show queries with errors or low scores.
	for _, q := range r.Queries {
		if q.Error != "" {
			fmt.Printf("  ⚠ %-40s ERROR: %s\n", q.Query, q.Error)
		} else if q.RecallAt10 < 0.5 {
			fmt.Printf("  ⚡ %-40s recall: %.2f  (found %d of %d relevant)\n",
				q.Query, q.RecallAt10, q.Found, q.Relevant)
		}
	}
}

func modelOrDefault(model string) string {
	if model != "" {
		return model
	}
	return "project default"
}

// register is internal — bench registers itself via the commands list in main.
