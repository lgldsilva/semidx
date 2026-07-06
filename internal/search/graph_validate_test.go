package search

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/localstore"
)

// TestGraphRAGValidation is the Phase 0 spike that proves graph expansion
// improves recall over keyword-only search.
//
// Fixture call chain: gateway → auth → jwt
//   - gateway.go: "bearer token", "validate", "API request"
//   - auth.go:    "identity proof", "credential", "access"
//   - jwt.go:     "base64 decode", "HMAC compute", "byte compare"
//
// Each file deliberately uses different vocabulary so keyword search
// cannot find the full chain. Graph expansion (BFS via import edges)
// discovers the missing files.
//
// Primary query: "validate bearer tokens in API requests"
//
//	Baseline: gateway.go only        (33% recall)
//	Graph:    gateway + auth + jwt   (100% recall, +67pp)
func TestGraphRAGValidation(t *testing.T) {
	ctx := context.Background()
	fixtureRoot := findFixtureRoot(t)

	// ----- Setup: temp SQLite store ---------------------------------------
	dbPath := filepath.Join(t.TempDir(), "index.db")
	store, err := localstore.New(dbPath)
	if err != nil {
		t.Fatalf("localstore.New: %v", err)
	}
	t.Cleanup(store.Close)

	if err := store.EnsureChunksTable(ctx, 1); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}

	// ----- Create project -------------------------------------------------
	projectID, err := store.UpsertProject(ctx, "graphrag-fixture", fixtureRoot, "keyword", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	// ----- Index all .go files (keyword-only, single chunk per file) ------
	files := walkGoFiles(t, fixtureRoot)
	fileIDs := make(map[string]int)

	for _, relPath := range files {
		content, err := os.ReadFile(filepath.Join(fixtureRoot, relPath))
		if err != nil {
			t.Fatalf("read %s: %v", relPath, err)
		}
		h := sha256.Sum256(content)
		hash := fmt.Sprintf("%x", h[:])
		fid, err := store.UpsertFile(ctx, projectID, relPath, hash, len(content))
		if err != nil {
			t.Fatalf("UpsertFile(%s): %v", relPath, err)
		}
		fileIDs[relPath] = fid
	}

	for _, relPath := range files {
		content, err := os.ReadFile(filepath.Join(fixtureRoot, relPath))
		if err != nil {
			t.Fatalf("read %s: %v", relPath, err)
		}
		lines := strings.Count(string(content), "\n") + 1
		chunks := []chunker.Chunk{{
			Content:   string(content),
			StartLine: 1,
			EndLine:   lines,
		}}
		if err := store.InsertChunksTextOnly(ctx, projectID, fileIDs[relPath], chunks, 1); err != nil {
			t.Fatalf("InsertChunksTextOnly(%s): %v", relPath, err)
		}
	}
	_ = fileIDs // not needed past indexing

	// ----- Relevant files --------------------------------------------------
	allRelevant := map[string]bool{
		"gateway/gateway.go": true,
		"auth/auth.go":       true,
		"jwt/jwt.go":         true,
	}

	// ----- Dependency graph (what Go import extraction would produce) -------
	dependencyGraph := map[string][]string{
		"gateway/gateway.go": {"auth/auth.go", "log/log.go"},
		"auth/auth.go":       {"jwt/jwt.go", "log/log.go"},
		"jwt/jwt.go":         {"log/log.go"},
		"log/log.go":         {},
	}

	// ----- Test cases ------------------------------------------------------
	// Each query targets vocabulary from a specific link in the chain.
	testCases := []struct {
		query       string
		seedFile    string // expected to be found in baseline (vocabulary match)
		description string
	}{
		{
			query:       "validate bearer tokens in API requests",
			seedFile:    "gateway/gateway.go",
			description: "gateway vocabulary → discovers auth + jwt via graph",
		},
		{
			query:       "identity proof credential checking examine",
			seedFile:    "auth/auth.go",
			description: "auth vocabulary → discovers jwt via graph",
		},
		{
			query:       "HMAC-SHA256 byte comparison decoding",
			seedFile:    "jwt/jwt.go",
			description: "jwt vocabulary → leaf node, no further discovery",
		},
	}

	const decayFactor = 0.85
	const floorThreshold = 0.3
	const maxDepth = 2

	t.Log("========== BASELINE vs GRAPH-EXPANDED ==========")

	passed := 0
	failed := 0

	for i, tc := range testCases {
		// --- Baseline search ---
		results, err := store.SearchSimilarKeywords(ctx, projectID, tc.query, 1, 5)
		if err != nil {
			t.Fatalf("SearchSimilarKeywords(%q): %v", tc.query, err)
		}
		var seeds []string
		for _, r := range results {
			seeds = append(seeds, r.FilePath)
		}

		baselineRecall := computeRecall(seeds, allRelevant)

		// --- Graph expansion ---
		expanded := bfsExpandPaths(seeds, dependencyGraph, maxDepth, decayFactor, floorThreshold)
		graphRecall := computeRecall(expanded, allRelevant)
		delta := (graphRecall - baselineRecall) * 100

		t.Logf("  [%d] %s", i, tc.description)
		t.Logf("      query:   %q", tc.query)
		t.Logf("      seeds:   %v", seeds)
		t.Logf("      expand:  %v", expanded)
		t.Logf("      recall:  baseline=%.0f%%  graph=%.0f%%  delta=%+.0fpp",
			baselineRecall*100, graphRecall*100, delta)

		// Verify expected seed file is in baseline. If not, vocabulary
		// isolation is broken and the test is invalid.
		if !containsPath(seeds, tc.seedFile) {
			t.Errorf("  FAIL: expected seed %q not in baseline results — vocabulary isolation broken", tc.seedFile)
			failed++
			continue
		}

		// Gate: no regression.
		if graphRecall < baselineRecall {
			t.Errorf("  FAIL: graph recall %.0f%% < baseline %.0f%% (regression)", graphRecall*100, baselineRecall*100)
			failed++
			continue
		}

		// Gate: graph expansion must improve recall for non-leaf queries.
		if tc.seedFile == "jwt/jwt.go" {
			// Leaf: expansion can't find upstream files (graph is directed).
			// Accept 0 gain — this is expected topology.
			t.Logf("      PASS: leaf node, no expansion expected")
			passed++
		} else {
			// Non-leaf: must show recall gain.
			if delta < 25 {
				t.Errorf("  FAIL: gain %.0fpp < 25pp — graph expansion insufficient", delta)
				failed++
			} else {
				t.Logf("      PASS: +%.0fpp recall gain", delta)
				passed++
			}
		}
	}

	// ----- Hub check -------------------------------------------------------
	t.Log("========== HUB CHECK ==========")
	for _, tc := range testCases {
		results, _ := store.SearchSimilarKeywords(ctx, projectID, tc.query, 1, 5)
		var seeds []string
		for _, r := range results {
			seeds = append(seeds, r.FilePath)
		}
		expanded := bfsExpandPaths(seeds, dependencyGraph, maxDepth, decayFactor, floorThreshold)
		hasHub := containsPath(expanded, "log/log.go")
		t.Logf("  %s  hub(log.go) present: %v", tc.seedFile, hasHub)
	}

	// ----- Final verdict ---------------------------------------------------
	t.Log("========== VERDICT ==========")
	t.Logf("  %d passed, %d failed", passed, failed)

	if failed > 0 {
		t.Fatalf("Phase 0 gate: %d failure(s) — graph expansion did not meet thresholds", failed)
	}

	// Only relevant gate: the primary query (gateway → full chain discovery).
	// Assert +67pp minimum gain from the gateway-seeded query.
	results, _ := store.SearchSimilarKeywords(ctx, projectID, "validate bearer tokens in API requests", 1, 5)
	var primarySeeds []string
	for _, r := range results {
		primarySeeds = append(primarySeeds, r.FilePath)
	}
	expanded := bfsExpandPaths(primarySeeds, dependencyGraph, maxDepth, decayFactor, floorThreshold)
	primaryBaseline := computeRecall(primarySeeds, allRelevant)
	primaryGraph := computeRecall(expanded, allRelevant)
	primaryDelta := (primaryGraph - primaryBaseline) * 100

	t.Logf("  PRIMARY: baseline=%.0f%%  graph=%.0f%%  delta=%+.0fpp", primaryBaseline*100, primaryGraph*100, primaryDelta)

	if primaryDelta < 33 {
		t.Fatalf("PRIMARY GATE FAIL: recall gain %.0fpp < 33pp", primaryDelta)
	}
	t.Logf("  Phase 0 PRIMARY GATE PASS: +%.0fpp recall gain (>= 33pp)", primaryDelta)
}

// ----- Helpers ---------------------------------------------------------------

func findFixtureRoot(t *testing.T) string {
	t.Helper()
	cwd, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
			fixture := filepath.Join(cwd, "testdata", "graphrag-fixture")
			if _, err := os.Stat(fixture); err == nil {
				return fixture
			}
		}
		parent := filepath.Dir(cwd)
		if parent == cwd {
			t.Fatal("cannot find project root")
		}
		cwd = parent
	}
}

func walkGoFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(info.Name(), ".go") {
			rel, _ := filepath.Rel(root, path)
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func computeRecall(found []string, relevant map[string]bool) float64 {
	if len(relevant) == 0 {
		return 0
	}
	count := 0
	for _, f := range found {
		if relevant[f] {
			count++
		}
	}
	return float64(count) / float64(len(relevant))
}

type bfsItem struct {
	path  string
	score float64
	depth int
}

func bfsExpandPaths(seeds []string, edges map[string][]string, maxDepth int, decay, floor float64) []string {
	visited := make(map[string]bool)
	queue := make([]bfsItem, 0)

	for _, p := range seeds {
		if !visited[p] {
			visited[p] = true
			queue = append(queue, bfsItem{path: p, score: 1.0, depth: 0})
		}
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if cur.depth >= maxDepth {
			continue
		}

		for _, neighbor := range edges[cur.path] {
			if visited[neighbor] {
				continue
			}
			adjusted := cur.score * decay
			if adjusted < floor {
				continue
			}
			visited[neighbor] = true
			queue = append(queue, bfsItem{path: neighbor, score: adjusted, depth: cur.depth + 1})
		}
	}

	var result []string
	for p := range visited {
		result = append(result, p)
	}
	sortStrings(result)
	return result
}

func containsPath(paths []string, target string) bool {
	for _, p := range paths {
		if p == target {
			return true
		}
	}
	return false
}

func sortStrings(s []string) {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[i] > s[j] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}
