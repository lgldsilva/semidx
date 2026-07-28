// Package eval contains deterministic evaluation primitives for semantic
// retrieval and agent-context experiments. It deliberately has no dependency
// on the search runtime or on a specific embedding provider.
package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const CurrentDatasetVersion = 2

// Dataset is the versioned ground-truth input for a retrieval benchmark.
type Dataset struct {
	Version int     `json:"version"`
	Queries []Query `json:"queries"`
}

// Query describes one user intent and its graded relevant files.
type Query struct {
	ID            string      `json:"id"`
	Query         string      `json:"query"`
	Description   string      `json:"description,omitempty"`
	Intent        string      `json:"intent,omitempty"`
	Language      string      `json:"language,omitempty"`
	ProjectRef    string      `json:"project_ref,omitempty"`
	Relevant      []Relevance `json:"relevant"`
	NegativeFiles []string    `json:"negative_files,omitempty"`
}

// Relevance is a graded file-level judgment. Grade 3 is the implementation
// that answers the query, grade 2 is directly necessary context, and grade 1
// is useful but insufficient context.
type Relevance struct {
	File      string   `json:"file"`
	Grade     int      `json:"grade"`
	Symbols   []string `json:"symbols,omitempty"`
	LineStart int      `json:"line_start,omitempty"`
	LineEnd   int      `json:"line_end,omitempty"`
}

// Ranked is the smallest adapter needed by the metrics package.
type Ranked struct {
	File string
}

// QueryMetrics contains per-query retrieval measurements.
type QueryMetrics struct {
	ID         string  `json:"id"`
	Query      string  `json:"query"`
	NDCG10     float64 `json:"ndcg_at_10"`
	MRR        float64 `json:"mrr"`
	Precision5 float64 `json:"precision_at_5"`
	Recall10   float64 `json:"recall_at_10"`
	Found      int     `json:"results_found"`
	Relevant   int     `json:"relevant_total"`
	Fallback   bool    `json:"fallback,omitempty"`
	Degraded   bool    `json:"degraded,omitempty"`
	Error      string  `json:"error,omitempty"`
}

// RunMetadata makes a result comparable to another run. Empty values are
// allowed for callers that do not have a local git or index fingerprint.
type RunMetadata struct {
	Commit           string `json:"commit,omitempty"`
	DatasetSHA256    string `json:"dataset_sha256,omitempty"`
	Backend          string `json:"backend,omitempty"`
	Model            string `json:"model,omitempty"`
	Dimensions       int    `json:"dimensions,omitempty"`
	Project          string `json:"project,omitempty"`
	ProjectIdentity  string `json:"project_identity,omitempty"`
	Worktree         string `json:"worktree,omitempty"`
	IndexFingerprint string `json:"index_fingerprint,omitempty"`
	Mode             string `json:"mode,omitempty"`
	Environment      string `json:"environment,omitempty"`
	Seed             int64  `json:"seed,omitempty"`
	Runs             int    `json:"runs,omitempty"`
}

// Results is the aggregate artifact written by a benchmark.
type Results struct {
	Version      int            `json:"version"`
	Metadata     RunMetadata    `json:"metadata"`
	Total        int            `json:"total_queries"`
	Failed       int            `json:"failed_queries"`
	Fallbacks    int            `json:"fallback_queries"`
	NDCG10       float64        `json:"ndcg_at_10"`
	MRR          float64        `json:"mrr"`
	Precision5   float64        `json:"precision_at_5"`
	Recall10     float64        `json:"recall_at_10"`
	LatencyP50MS float64        `json:"latency_p50_ms"`
	LatencyP95MS float64        `json:"latency_p95_ms"`
	LatencyP99MS float64        `json:"latency_p99_ms"`
	Queries      []QueryMetrics `json:"queries"`
}

// LoadDataset reads either the v2 object form or the legacy array form used by
// docs/bench-queries.json. Legacy entries are converted to grade-1 relevance so
// existing users can migrate their files without a flag-day change.
func LoadDataset(path string) (Dataset, error) {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return Dataset{}, fmt.Errorf("read dataset: %w", err)
	}
	var ds Dataset
	if err := json.Unmarshal(b, &ds); err == nil && ds.Queries != nil {
		if ds.Version == 0 {
			ds.Version = CurrentDatasetVersion
		}
		if err := Validate(ds); err != nil {
			return Dataset{}, err
		}
		return ds, nil
	}
	var legacy []struct {
		Query         string   `json:"query"`
		RelevantFiles []string `json:"relevant_files"`
		Description   string   `json:"description,omitempty"`
	}
	if err := json.Unmarshal(b, &legacy); err != nil {
		return Dataset{}, fmt.Errorf("parse dataset: %w", err)
	}
	ds.Version = 1
	ds.Queries = make([]Query, len(legacy))
	for i, q := range legacy {
		relevant := make([]Relevance, len(q.RelevantFiles))
		for j, f := range q.RelevantFiles {
			relevant[j] = Relevance{File: f, Grade: 1}
		}
		ds.Queries[i] = Query{ID: fmt.Sprintf("legacy-%03d", i+1), Query: q.Query, Description: q.Description, Relevant: relevant}
	}
	if err := Validate(ds); err != nil {
		return Dataset{}, err
	}
	return ds, nil
}

// DatasetSHA256 returns the hash of the canonical JSON representation. The
// hash is used as a comparability guard, not as a security credential.
func DatasetSHA256(ds Dataset) (string, error) {
	b, err := json.Marshal(ds)
	if err != nil {
		return "", fmt.Errorf("marshal dataset: %w", err)
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// Validate checks the invariants that make a dataset safe for a benchmark.
func Validate(ds Dataset) error {
	if ds.Version < 1 || ds.Version > CurrentDatasetVersion {
		return fmt.Errorf("unsupported dataset version %d", ds.Version)
	}
	if len(ds.Queries) == 0 {
		return fmt.Errorf("dataset has no queries")
	}
	ids := make(map[string]struct{}, len(ds.Queries))
	for i, q := range ds.Queries {
		if strings.TrimSpace(q.ID) == "" {
			return fmt.Errorf("query %d has empty id", i)
		}
		if _, ok := ids[q.ID]; ok {
			return fmt.Errorf("duplicate query id %q", q.ID)
		}
		ids[q.ID] = struct{}{}
		if strings.TrimSpace(q.Query) == "" {
			return fmt.Errorf("query %q has empty text", q.ID)
		}
		if len(q.Relevant) == 0 {
			return fmt.Errorf("query %q has no relevant files", q.ID)
		}
		files := make(map[string]struct{}, len(q.Relevant))
		for j, r := range q.Relevant {
			if err := validateRelativeFile(r.File); err != nil {
				return fmt.Errorf("query %q relevant[%d]: %w", q.ID, j, err)
			}
			if r.Grade < 1 || r.Grade > 3 {
				return fmt.Errorf("query %q relevant[%d] grade %d outside 1..3", q.ID, j, r.Grade)
			}
			if _, ok := files[r.File]; ok {
				return fmt.Errorf("query %q repeats relevant file %q", q.ID, r.File)
			}
			files[r.File] = struct{}{}
			if r.LineStart < 0 || r.LineEnd < 0 || (r.LineStart > 0 && r.LineEnd > 0 && r.LineEnd < r.LineStart) {
				return fmt.Errorf("query %q relevant[%d] has invalid line range", q.ID, j)
			}
		}
		for j, f := range q.NegativeFiles {
			if err := validateRelativeFile(f); err != nil {
				return fmt.Errorf("query %q negative_files[%d]: %w", q.ID, j, err)
			}
			if _, ok := files[f]; ok {
				return fmt.Errorf("query %q marks relevant file %q as negative", q.ID, f)
			}
		}
	}
	return nil
}

// ValidateFiles verifies that every judged path exists as a regular file under
// the selected corpus root. Structural validation remains separate so datasets
// can still be inspected before a corpus checkout is available.
func ValidateFiles(ds Dataset, root string) error {
	if err := Validate(ds); err != nil {
		return err
	}
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("stat dataset project root: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("dataset project root is not a directory: %s", root)
	}
	for _, query := range ds.Queries {
		for _, relevant := range query.Relevant {
			if err := validateCorpusFile(root, relevant.File); err != nil {
				return fmt.Errorf("query %q relevant file: %w", query.ID, err)
			}
		}
		for _, negative := range query.NegativeFiles {
			if err := validateCorpusFile(root, negative); err != nil {
				return fmt.Errorf("query %q negative file: %w", query.ID, err)
			}
		}
	}
	return nil
}

func validateCorpusFile(root, relative string) error {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		return fmt.Errorf("%s: %w", relative, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", relative)
	}
	return nil
}

func validateRelativeFile(file string) error {
	if strings.TrimSpace(file) == "" {
		return fmt.Errorf("file is empty")
	}
	if filepath.IsAbs(file) || filepath.Clean(file) == "." || strings.HasPrefix(filepath.ToSlash(filepath.Clean(file)), "../") {
		return fmt.Errorf("file must be project-relative: %q", file)
	}
	return nil
}

// Grades returns a stable file-to-grade map for metric calculation.
func Grades(q Query) map[string]int {
	out := make(map[string]int, len(q.Relevant))
	for _, r := range q.Relevant {
		out[r.File] = r.Grade
	}
	return out
}
