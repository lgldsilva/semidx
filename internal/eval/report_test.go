package eval

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportRoundTripAndCompareGuards(t *testing.T) {
	base := Results{Version: CurrentDatasetVersion, Metadata: RunMetadata{DatasetSHA256: "ds", IndexFingerprint: "idx"}, NDCG10: 0.4, RouteCounts: map[string]int{"keyword": 2, "hybrid": 3}, Queries: []QueryMetrics{{ID: "q1", Route: "hybrid"}, {ID: "q2", Route: "vector"}}}
	candidate := base
	candidate.NDCG10 = 0.6
	candidate.RouteCounts = map[string]int{"keyword": 1, "hybrid": 4}
	candidate.Queries = []QueryMetrics{{ID: "q1", Route: "vector"}, {ID: "q2", Route: "hybrid"}}
	b, err := MarshalResults(base)
	if err != nil || !strings.HasSuffix(string(b), "\n") {
		t.Fatalf("marshal err=%v output=%q", err, b)
	}
	delta, err := Compare(base, candidate)
	if err != nil || math.Abs(delta.NDCG10-0.2) > 1e-9 || delta.RouteCounts["keyword"] != -1 || delta.RouteCounts["hybrid"] != 1 || delta.RouteTransitions["hybrid->vector"] != 1 || delta.RouteTransitions["vector->hybrid"] != 1 {
		t.Fatalf("delta=%+v err=%v", delta, err)
	}
	candidate.Metadata.DatasetSHA256 = "other"
	if _, err := Compare(base, candidate); err == nil {
		t.Fatal("expected dataset compatibility error")
	}

	path := filepath.Join(t.TempDir(), "result.json")
	if err := WriteResults(path, base); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadResults(path)
	if err != nil || loaded.Metadata.DatasetSHA256 != "ds" {
		t.Fatalf("LoadResults = %+v, err %v", loaded, err)
	}
}

func TestCompareRejectsMissingComparisonMetadata(t *testing.T) {
	base := Results{}
	if _, err := Compare(base, base); err == nil {
		t.Fatal("comparison without dataset and index fingerprints must fail")
	}
	base.Metadata.DatasetSHA256 = "ds"
	if _, err := Compare(base, base); err == nil {
		t.Fatal("comparison without index fingerprint must fail")
	}
}

func TestReportRejectsMalformedArtifactsAndWriteErrors(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"malformed": `{"version":`,
		"trailing":  `{"version":2} {"version":2}`,
		"future":    `{"version":99}`,
	} {
		path := filepath.Join(dir, name+".json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadResults(path); err == nil {
			t.Errorf("LoadResults(%s) unexpectedly succeeded", name)
		}
	}
	if _, err := LoadResults(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("missing artifact should fail")
	}
	if err := WriteResults(dir, Results{}); err == nil {
		t.Fatal("writing an artifact over a directory should fail")
	}
}

func TestCompareRejectsFingerprintMismatch(t *testing.T) {
	base := Results{Metadata: RunMetadata{DatasetSHA256: "ds", IndexFingerprint: "a"}}
	candidate := Results{Metadata: RunMetadata{DatasetSHA256: "ds", IndexFingerprint: "b"}}
	if _, err := Compare(base, candidate); err == nil {
		t.Fatal("expected fingerprint compatibility error")
	}
}

func TestCompareRejectsRuntimeContractMismatch(t *testing.T) {
	base := Results{Metadata: RunMetadata{
		DatasetSHA256: "ds", IndexFingerprint: "idx", Backend: "sqlite",
		Model: "bge-m3", Dimensions: 1024, ProjectIdentity: "git:app",
		Worktree: "/work/app", Mode: "hybrid",
	}}
	for name, mutate := range map[string]func(*Results){
		"backend":          func(r *Results) { r.Metadata.Backend = "postgres" },
		"model":            func(r *Results) { r.Metadata.Model = "other" },
		"dimensions":       func(r *Results) { r.Metadata.Dimensions = 768 },
		"project identity": func(r *Results) { r.Metadata.ProjectIdentity = "git:other" },
		"mode":             func(r *Results) { r.Metadata.Mode = "keyword" },
		"seed":             func(r *Results) { r.Metadata.Seed = 99 },
		"runs":             func(r *Results) { r.Metadata.Runs = 5 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			mutate(&candidate)
			if _, err := Compare(base, candidate); err == nil {
				t.Fatalf("comparison accepted mismatched %s", name)
			}
		})
	}
}

func TestCompareAllowsDifferentCheckoutPathsForSameCorpus(t *testing.T) {
	base := Results{Metadata: RunMetadata{
		DatasetSHA256: "ds", IndexFingerprint: "idx", Backend: "sqlite",
		Model: "bge-m3", Dimensions: 1024, ProjectIdentity: "git:app",
		Worktree: "/work/base", Mode: "hybrid",
	}}
	candidate := base
	candidate.Metadata.Worktree = "/work/candidate"
	if _, err := Compare(base, candidate); err != nil {
		t.Fatalf("same corpus in another checkout should remain comparable: %v", err)
	}
}
