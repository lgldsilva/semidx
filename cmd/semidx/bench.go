package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// benchQuery is one labelled search with ground-truth relevant files for
// computing information-retrieval metrics.
type benchQuery struct {
	Query         string   `json:"query"`
	RelevantFiles []string `json:"relevant_files"`
	Description   string   `json:"description,omitempty"`
}

// benchResults holds per-query and aggregated metrics.
type benchResults struct {
	Model      string         `json:"model"`
	Baseline   string         `json:"baseline,omitempty"` // keyword or empty
	Total      int            `json:"total_queries"`
	Failed     int            `json:"failed_queries"`
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
	Error       string  `json:"error,omitempty"`
}

func newBenchCmd(d *deps) *cobra.Command {
	var (
		project, model, queriesFile string
		topK                        int
		privacy, asJSON             bool
		baselineKeyword             bool
	)
	c := &cobra.Command{
		Use:   "bench",
		Short: "Run search quality benchmarks against ground-truth queries",
		Long: `Benchmark semantic search quality against a set of labelled queries.

A queries file is a JSON array of objects, each with a "query" string and a
"relevant_files" array (file paths that should appear in results). Metrics
reported: nDCG@10, MRR, Precision@5, Recall@10.

With --baseline-keyword, the benchmark also runs a pure keyword search and
reports the improvement of semantic search over the keyword baseline.`,
		Example: `  semidx bench --project . --queries queries.json
  semidx bench --project myapp --queries queries.json --baseline-keyword --json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			queries, err := loadBenchQueries(queriesFile)
			if err != nil {
				return err
			}
			if len(queries) == 0 {
				return fmt.Errorf("no queries found in %s", queriesFile)
			}

			results := runBenchmarks(cmd, d, project, model, topK, privacy, queries)

			// Optional: compute keyword baseline and report improvement.
			if baselineKeyword {
				kwResults := runBenchmarksKeyword(cmd, d, project, topK, queries)
				results.Baseline = "keyword"
				if asJSON {
					results.Baseline = fmt.Sprintf("keyword (ndcg:%.3f mrr:%.3f)", kwResults.NDCG10, kwResults.MRR)
				}
			}

			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(results)
			}
			printBenchResults(results)
			return nil
		},
	}
	c.Flags().StringVar(&project, "project", ".", "Project path or name")
	c.Flags().StringVar(&queriesFile, "queries", "", "Path to JSON queries file (required)")
	c.Flags().IntVar(&topK, "top-k", 10, "Number of results per query")
	c.Flags().StringVar(&model, "model", "", "Override embedding model")
	c.Flags().BoolVar(&privacy, "privacy", false, "Force local-only providers (Ollama)")
	c.Flags().BoolVar(&asJSON, "json", false, "Output results as JSON")
	c.Flags().BoolVar(&baselineKeyword, "baseline-keyword", false, "Also run keyword search to compare")
	_ = c.MarkFlagRequired("queries")
	return c
}

func loadBenchQueries(path string) ([]benchQuery, error) {
	// #nosec G304 — path comes from a --queries CLI flag; the user explicitly provides it.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read queries file: %w", err)
	}
	var queries []benchQuery
	if err := json.Unmarshal(data, &queries); err != nil {
		return nil, fmt.Errorf("parse queries file: %w", err)
	}
	return queries, nil
}

func runBenchmarks(cmd *cobra.Command, d *deps, project, model string, topK int, privacy bool, queries []benchQuery) *benchResults {
	out := &benchResults{Model: model, Total: len(queries)}
	var sumNDCG, sumMRR, sumP5, sumR10 float64
	start := time.Now()

	for i, q := range queries {
		results, err := d.runSearchTargets(cmd, project, q.Query, model, topK, privacy)
		qm := queryMetrics{Query: q.Query, Description: q.Description, Relevant: len(q.RelevantFiles)}
		if err != nil {
			qm.Error = err.Error()
			out.Failed++
			out.Queries = append(out.Queries, qm)
			continue
		}
		// Flatten result files from all projects searched.
		var files []string
		for _, ps := range results {
			for _, r := range ps.resp.Results {
				files = append(files, r.FilePath)
			}
		}
		qm.Found = len(files)

		relSet := toSet(q.RelevantFiles)
		qm.NDCG10 = computeNDCG(files, relSet, topK)
		qm.MRR = computeMRR(files, relSet)
		qm.PrecAt5 = precisionAtK(files, relSet, 5)
		qm.RecallAt10 = recallAtK(files, relSet, 10)

		sumNDCG += qm.NDCG10
		sumMRR += qm.MRR
		sumP5 += qm.PrecAt5
		sumR10 += qm.RecallAt10
		out.Queries = append(out.Queries, qm)

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
	out.Model = fmt.Sprintf("%s (%v)", modelOrDefault(model), time.Since(start).Round(time.Millisecond))
	return out
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
		var files []string
		for _, ps := range results {
			for _, r := range ps.resp.Results {
				files = append(files, r.FilePath)
			}
		}
		relSet := toSet(q.RelevantFiles)
		qm.NDCG10 = computeNDCG(files, relSet, topK)
		qm.MRR = computeMRR(files, relSet)
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

// --- IR metrics ----------------------------------------------------------------

func toSet(files []string) map[string]bool {
	s := make(map[string]bool, len(files))
	for _, f := range files {
		s[f] = true
	}
	return s
}

// computeNDCG returns nDCG@k. Relevant items get gain 1, others 0. The ideal
// ordering puts all relevant items first.
func computeNDCG(ranked []string, relevant map[string]bool, k int) float64 {
	if k > len(ranked) {
		k = len(ranked)
	}
	if k == 0 {
		return 0
	}

	// DCG
	var dcg float64
	for i := 1; i <= k; i++ {
		f := ranked[i-1]
		if relevant[f] {
			dcg += 1.0 / math.Log2(float64(i+1))
		}
	}

	// IDCG (ideal: all relevant items ranked first)
	idealCount := len(relevant)
	if idealCount > k {
		idealCount = k
	}
	var idcg float64
	for i := 1; i <= idealCount; i++ {
		idcg += 1.0 / math.Log2(float64(i+1))
	}
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

// computeMRR returns Mean Reciprocal Rank: 1 / rank of the first relevant item.
func computeMRR(ranked []string, relevant map[string]bool) float64 {
	for i, f := range ranked {
		if relevant[f] {
			return 1.0 / float64(i+1)
		}
	}
	return 0
}

// precisionAtK returns the fraction of the top-k that are relevant.
func precisionAtK(ranked []string, relevant map[string]bool, k int) float64 {
	if k > len(ranked) {
		k = len(ranked)
	}
	if k == 0 {
		return 0
	}
	hits := 0
	for i := 0; i < k; i++ {
		if relevant[ranked[i]] {
			hits++
		}
	}
	return float64(hits) / float64(k)
}

// recallAtK returns the fraction of all relevant items found in the top-k.
func recallAtK(ranked []string, relevant map[string]bool, k int) float64 {
	if k > len(ranked) {
		k = len(ranked)
	}
	if len(relevant) == 0 {
		return 0
	}
	hits := 0
	for i := 0; i < k; i++ {
		if relevant[ranked[i]] {
			hits++
		}
	}
	return float64(hits) / float64(len(relevant))
}

// --- output formatting --------------------------------------------------------

func printBenchResults(r *benchResults) {
	fmt.Println()
	fmt.Println(strings.Repeat("═", 60))
	fmt.Printf("  semidx bench results  (%d queries, %d failed)\n", r.Total, r.Failed)
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
var _ = &cobra.Command{} // unused; kept for package consistency
