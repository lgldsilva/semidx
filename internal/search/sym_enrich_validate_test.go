package search

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/analyzer"
	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/localstore"
	"github.com/lgldsilva/semidx/internal/store"
)

// TestSymbolEnrichValidation tests whether prefixing chunk embeddings with
// extracted symbol names improves search relevance.
//
// It indexes the semidx codebase with and without symbol enrichment,
// then runs gold-standard queries and compares nDCG@5.
func TestSymbolEnrichValidation(t *testing.T) {
	ollamaURL := os.Getenv("OLLAMA_URL")
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}
	emb := embed.NewOllamaClient(ollamaURL)
	model := "nomic-embed-text"

	ctx := context.Background()
	if _, err := emb.ModelInfo(ctx, model); err != nil {
		t.Skipf("Ollama nomic-embed-text not available: %v", err)
	}

	root := findSymRoot(t)
	files := walkSymFiles(t, root)

	if len(files) == 0 {
		t.Fatal("no Go files found")
	}
	t.Logf("found %d Go files", len(files))

	// ----- Gold-standard queries -------------------------------------------
	type goldQuery struct {
		query    string
		relevant []string // prefix-matched against result file paths
	}
	queries := []goldQuery{
		{"where do we validate JWT tokens", []string{"jwtauth/jwtauth.go"}},
		{"how is the embedding chain provider routing done", []string{"internal/embed/chain.go"}},
		{"where are SQLite chunks stored and queried locally", []string{"internal/localstore/localstore.go"}},
		{"how does the chunker split code into pieces", []string{"internal/chunker/chunker.go"}},
		{"where is the MCP server search tool defined", []string{"internal/mcpserver"}},
		{"how are project identities resolved from git", []string{"internal/gitmeta/gitmeta.go"}},
		{"where is privacy sensitive file detection", []string{"internal/privacy/privacy.go"}},
		{"how does the indexer walk and index project files", []string{"internal/indexing/indexer.go"}},
		{"where is password-protected PDF and Office extraction", []string{"internal/extract/crypto.go"}},
		{"how are import dependencies analyzed per language", []string{"internal/imports/imports.go"}},
	}

	// ----- Index baseline (no symbols) -------------------------------------
	baseStore := newSymStore(t)
	indexSymFiles(t, ctx, baseStore, emb, model, root, files, false, "sym-base")
	baseSvc := NewService(baseStore, emb)

	// ----- Index enriched (with symbols) -----------------------------------
	enrStore := newSymStore(t)
	indexSymFiles(t, ctx, enrStore, emb, model, root, files, true, "sym-enrich")
	enrSvc := NewService(enrStore, emb)

	// ----- Run queries and compare -----------------------------------------
	type qResult struct {
		query string
		bNDCG float64
		eNDCG float64
		delta float64
		sign  string
	}
	var results []qResult
	const baseProject = "sym-base"
	const enrProject = "sym-enrich"

	for i, q := range queries {
		baseHits := searchSymTopK(t, ctx, baseSvc, baseProject, q.query, 5)
		enrHits := searchSymTopK(t, ctx, enrSvc, enrProject, q.query, 5)

		bScore := computeNDCG(baseHits, q.relevant)
		eScore := computeNDCG(enrHits, q.relevant)
		delta := eScore - bScore

		sign := "="
		switch {
		case delta > 0.01:
			sign = "+"
		case delta < -0.01:
			sign = "-"
		}

		results = append(results, qResult{q.query, bScore, eScore, delta, sign})

		t.Logf("[q%d] %s", i+1, q.query)
		t.Logf("      baseline nDCG@5=%.3f  enriched nDCG@5=%.3f  Δ=%+.3f %s",
			bScore, eScore, delta, sign)
		for _, rel := range q.relevant {
			bf := bestMatchOne(rel, baseHits)
			ef := bestMatchOne(rel, enrHits)
			t.Logf("      %-7s baseline=%s  enriched=%s", rel, bf, ef)
		}
	}

	// ----- Report ----------------------------------------------------------
	var wins, losses, ties int
	var sumDelta float64
	for _, r := range results {
		switch {
		case r.delta > 0.01:
			wins++
		case r.delta < -0.01:
			losses++
		default:
			ties++
		}
		sumDelta += r.delta
	}
	avgDelta := sumDelta / float64(len(results))

	t.Logf("---")
	t.Logf("Summary: %d queries — %d wins, %d losses, %d ties, avg Δ=%+.3f",
		len(results), wins, losses, ties, avgDelta)

	// Gate: require non-negative average delta (no regression)
	if avgDelta < -0.01 {
		t.Errorf("average nDCG delta is negative: %.3f — symbol enrichment degrades relevance", avgDelta)
	}
	if wins > losses {
		t.Logf("symbol enrichment shows positive signal (%d wins vs %d losses)", wins, losses)
	}
}

// newSymStore creates a temporary SQLite store with an ensured chunks table.
func newSymStore(t *testing.T) *localstore.SQLiteStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.db")
	s, err := localstore.New(path)
	if err != nil {
		t.Fatalf("localstore.New: %v", err)
	}
	t.Cleanup(s.Close)
	if err := s.EnsureChunksTable(context.Background(), 768); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}
	return s
}

// indexSymFiles indexes Go files into a store, optionally enriching chunks with symbols.
func indexSymFiles(t *testing.T, ctx context.Context, st *localstore.SQLiteStore,
	emb embed.Embedder, model, root string, files []string, enrich bool, label string) {
	t.Helper()

	pid, err := st.UpsertProject(ctx, label, root, model, 768)
	if err != nil {
		t.Fatalf("UpsertProject(%s): %v", label, err)
	}

	for _, rel := range files {
		content, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(content))

		fid, err := st.UpsertFile(ctx, pid, rel, hash, len(content))
		if err != nil {
			continue
		}

		chunks := chunker.ChunkFile(rel, content, 4000)
		if len(chunks) == 0 {
			continue
		}

		var syms []analyzer.Symbol
		if enrich {
			syms = analyzer.Symbols(rel, content)
		}

		inputs := make([]string, len(chunks))
		for i, c := range chunks {
			if len(syms) > 0 {
				var chunkSyms []string
				for _, s := range syms {
					if s.StartLine <= c.EndLine && s.EndLine >= c.StartLine {
						chunkSyms = append(chunkSyms, s.Kind+" "+s.Name)
					}
				}
				if len(chunkSyms) > 20 {
					chunkSyms = chunkSyms[:20]
				}
				if len(chunkSyms) > 0 {
					inputs[i] = "Symbols: " + strings.Join(chunkSyms, ", ") + "\n" + c.Content
				} else {
					inputs[i] = c.Content
				}
			} else {
				inputs[i] = c.Content
			}
		}

		embeddings, err := emb.Embed(ctx, model, inputs...)
		if err != nil {
			continue
		}
		if err := st.DeleteChunksForFile(ctx, pid, fid, 768); err != nil {
			t.Fatalf("DeleteChunksForFile(%s): %v", rel, err)
		}
		if err := st.InsertChunks(ctx, pid, fid, chunks, embeddings, 768); err != nil {
			t.Fatalf("InsertChunks(%s): %v", rel, err)
		}
	}

	t.Logf("%s: indexed %d files", label, len(files))
}

// searchSymTopK runs a search query and returns top-k file paths.
func searchSymTopK(t *testing.T, ctx context.Context, svc *Service, project, query string, k int) []string {
	t.Helper()
	resp, err := svc.Search(ctx, Request{Project: project, Query: query, TopK: k})
	if err != nil {
		t.Fatalf("search %q: %v", query, err)
	}
	var paths []string
	for _, r := range resp.Results {
		paths = append(paths, r.FilePath)
	}
	return paths
}

// computeNDCG computes normalized DCG@len(results) for ranked results.
func computeNDCG(results []string, relevant []string) float64 {
	if len(results) == 0 {
		return 0
	}
	var dcg float64
	for i, p := range results {
		rel := relevance(p, relevant)
		dcg += float64(rel) / math.Log2(float64(i+2))
	}
	var idcg float64
	for i := 0; i < len(relevant) && i < len(results); i++ {
		idcg += 1.0 / math.Log2(float64(i+2))
	}
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

// relevance returns 1 if path starts with any relevant prefix.
func relevance(path string, relevant []string) int {
	for _, r := range relevant {
		if strings.HasPrefix(path, r) {
			return 1
		}
	}
	return 0
}

// bestMatchOne reports the rank of a relevant target in results, or "no".
func bestMatchOne(target string, results []string) string {
	for i, p := range results {
		if strings.HasPrefix(p, target) {
			return fmt.Sprintf("rank #%d", i+1)
		}
	}
	return "no"
}

// walkSymFiles recursively finds non-test .go files excluding testdata/vendor.
func walkSymFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		base := d.Name()
		if d.IsDir() {
			if base == "testdata" || base == "vendor" || base == ".git" ||
				base == "node_modules" || strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(base, ".go") && !strings.HasSuffix(base, "_test.go") {
			rel, _ := filepath.Rel(root, p)
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}

// Compile-time check: SQLiteStore implements store.IndexStore.
var _ store.IndexStore = (*localstore.SQLiteStore)(nil)

// findSymRoot walks up from the test binary cwd to find go.mod (semidx root).
func findSymRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod (semidx root)")
		}
		dir = parent
	}
}
