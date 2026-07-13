package search

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/localstore"
	"github.com/lgldsilva/semidx/internal/store"
)

// graphFixture holds a real SQLite store with a project seeded with files,
// chunks and dependency edges. Tests that exercise expandByGraph use this.
type graphFixture struct {
	ctx context.Context
	st  *localstore.SQLiteStore
	pid int
	svc *Service
}

// createFile is a helper that inserts a file row and a single chunk.
func (g *graphFixture) createFile(t *testing.T, path, content string) {
	t.Helper()
	h := sha256.Sum256([]byte(content))
	hash := fmt.Sprintf("%x", h[:])
	fid, err := g.st.UpsertFile(g.ctx, g.pid, path, hash, len(content))
	if err != nil {
		t.Fatalf("UpsertFile(%s): %v", path, err)
	}
	if err := g.st.InsertChunksTextOnly(g.ctx, g.pid, fid, []chunker.Chunk{
		{Content: content, StartLine: 1, EndLine: 1},
	}, 1); err != nil {
		t.Fatalf("InsertChunksTextOnly(%s): %v", path, err)
	}
}

// addEdge is a helper that inserts a dependency edge.
func (g *graphFixture) addEdge(t *testing.T, src string, targets ...string) {
	t.Helper()
	if err := g.st.InsertFileDependencies(g.ctx, g.pid, src, targets); err != nil {
		t.Fatalf("InsertFileDependencies(%s): %v", src, err)
	}
}

func newGraphFixture(t *testing.T) *graphFixture {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "graph.db")
	st, err := localstore.New(dbPath)
	if err != nil {
		t.Fatalf("localstore.New: %v", err)
	}
	t.Cleanup(st.Close)

	pid, err := st.UpsertProject(ctx, "graph-proj", "/tmp/g", "keyword", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	svc := NewService(st, &fakeEmbedder{vec: []float32{1}, dims: 1})
	return &graphFixture{ctx: ctx, st: st, pid: pid, svc: svc}
}

// ---------------------------------------------------------------------------
// expandByGraph tests
// ---------------------------------------------------------------------------

func TestExpandByGraphBFSDiscoversDepth1And2(t *testing.T) {
	g := newGraphFixture(t)

	// Graph: a.go → b.go → c.go → d.go
	for _, p := range []string{"a.go", "b.go", "c.go", "d.go"} {
		g.createFile(t, p, "package p\nfunc F() {}")
	}
	g.addEdge(t, "a.go", "b.go")
	g.addEdge(t, "b.go", "c.go")
	g.addEdge(t, "c.go", "d.go")

	seeds := []store.SearchResult{{FilePath: "a.go", Content: "a", Score: 0.9}}
	results, err := g.svc.expandByGraph(g.ctx, &Request{GraphMaxDepth: 2}, seeds, g.pid, 1)
	if err != nil {
		t.Fatalf("expandByGraph: %v", err)
	}

	got := collectPaths(results)
	// b.go at depth=1, c.go at depth=2
	if !hasPath(got, "b.go") {
		t.Error("b.go (depth 1) should be discovered")
	}
	if !hasPath(got, "c.go") {
		t.Error("c.go (depth 2) should be discovered")
	}
	if hasPath(got, "a.go") {
		t.Error("seed a.go should not appear in expanded results")
	}
	if hasPath(got, "d.go") {
		t.Error("d.go (depth 3) must not be discovered with maxDepth=2")
	}
}

func TestExpandByGraphCycleDoesNotLoop(t *testing.T) {
	g := newGraphFixture(t)

	for _, p := range []string{"a.go", "b.go"} {
		g.createFile(t, p, "package p\nfunc F() {}")
	}
	// Cycle: a.go → b.go → a.go
	g.addEdge(t, "a.go", "b.go")
	g.addEdge(t, "b.go", "a.go")

	seeds := []store.SearchResult{{FilePath: "a.go", Content: "a", Score: 1.0}}
	results, err := g.svc.expandByGraph(g.ctx, &Request{GraphMaxDepth: 5}, seeds, g.pid, 1)
	if err != nil {
		t.Fatalf("expandByGraph: %v", err)
	}

	got := collectPaths(results)
	if !hasPath(got, "b.go") {
		t.Error("b.go should be discovered as neighbor of a.go")
	}
	if hasPath(got, "a.go") {
		t.Error("seed a.go should not appear in expanded results even if reached via cycle")
	}
	// The test passes if it completes without infinite loop — meaning visited
	// tracking works.  Only b.go is a valid expanded result.
	if len(got) != 1 || got[0] != "b.go" {
		t.Errorf("expected exactly b.go in results, got %v", got)
	}
}

func TestExpandByGraphDecayFactor(t *testing.T) {
	g := newGraphFixture(t)

	g.createFile(t, "seed.go", "package x\nfunc X() {}")
	g.createFile(t, "neighbor.go", "package y\nfunc Y() {}")
	g.addEdge(t, "seed.go", "neighbor.go")

	seeds := []store.SearchResult{{FilePath: "seed.go", Score: 1.0}}
	results, err := g.svc.expandByGraph(g.ctx, &Request{GraphMaxDepth: 1}, seeds, g.pid, 1)
	if err != nil {
		t.Fatalf("expandByGraph: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected at least one expanded result")
	}
	const decay = 0.85
	wantScore := 1.0 * decay
	for _, r := range results {
		if r.FilePath == "neighbor.go" {
			if r.Score != wantScore {
				t.Errorf("neighbor.go score = %v, want %v", r.Score, wantScore)
			}
			return
		}
	}
	t.Error("neighbor.go not found in expanded results")
}

func TestExpandByGraphFloorThreshold(t *testing.T) {
	g := newGraphFixture(t)

	g.createFile(t, "low.go", "package x\nfunc X() {}")
	g.createFile(t, "dropped.go", "package y\nfunc Y() {}")
	g.addEdge(t, "low.go", "dropped.go")

	// Seed score that decays below floor (0.3) after one hop.
	seeds := []store.SearchResult{{FilePath: "low.go", Score: 0.29}}
	results, err := g.svc.expandByGraph(g.ctx, &Request{GraphMaxDepth: 5}, seeds, g.pid, 1)
	if err != nil {
		t.Fatalf("expandByGraph: %v", err)
	}

	got := collectPaths(results)
	if hasPath(got, "dropped.go") {
		t.Errorf("dropped.go should be dropped because 0.29*0.85 < floor 0.3; got %v", got)
	}
	if len(got) != 0 {
		t.Errorf("expected no expanded results, got %v", got)
	}
}

func TestExpandByGraphMaxDepthLimit(t *testing.T) {
	g := newGraphFixture(t)

	for _, p := range []string{"a.go", "b.go", "c.go", "deep.go"} {
		g.createFile(t, p, "package p\nfunc F() {}")
	}
	g.addEdge(t, "a.go", "b.go")
	g.addEdge(t, "b.go", "c.go")
	g.addEdge(t, "c.go", "deep.go")

	seeds := []store.SearchResult{{FilePath: "a.go", Score: 1.0}}
	// maxDepth=1: only direct neighbors
	results, err := g.svc.expandByGraph(g.ctx, &Request{GraphMaxDepth: 1}, seeds, g.pid, 1)
	if err != nil {
		t.Fatalf("expandByGraph: %v", err)
	}

	got := collectPaths(results)
	if !hasPath(got, "b.go") {
		t.Error("b.go (depth 1) should be discovered")
	}
	if hasPath(got, "c.go") {
		t.Error("c.go (depth 2) should NOT be discovered with maxDepth=1")
	}
	if hasPath(got, "deep.go") {
		t.Error("deep.go (depth 3) should NOT be discovered with maxDepth=1")
	}
}

func TestExpandByGraphEmptyGraph(t *testing.T) {
	g := newGraphFixture(t)

	// Project with files but NO dependency edges.
	g.createFile(t, "orphan.go", "package p\nfunc F() {}")

	seeds := []store.SearchResult{{FilePath: "orphan.go", Score: 0.9}}
	results, err := g.svc.expandByGraph(g.ctx, &Request{GraphMaxDepth: 2}, seeds, g.pid, 1)
	if err != nil {
		t.Fatalf("expandByGraph with empty graph should not error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results for empty graph, got %v", results)
	}
}

func TestExpandByGraphSeedsDeduplicatedAsNeighbors(t *testing.T) {
	g := newGraphFixture(t)

	for _, p := range []string{"a.go", "b.go", "c.go"} {
		g.createFile(t, p, "package p\nfunc F() {}")
	}
	// a.go depends on b.go and c.go; c.go also depends on b.go.
	g.addEdge(t, "a.go", "b.go")
	g.addEdge(t, "a.go", "c.go")
	g.addEdge(t, "c.go", "b.go")

	// Both a.go and b.go are seeds — b.go is also a neighbor of a.go.
	seeds := []store.SearchResult{
		{FilePath: "a.go", Score: 1.0},
		{FilePath: "b.go", Score: 0.9},
	}
	results, err := g.svc.expandByGraph(g.ctx, &Request{GraphMaxDepth: 2}, seeds, g.pid, 1)
	if err != nil {
		t.Fatalf("expandByGraph: %v", err)
	}

	got := collectPaths(results)
	if hasPath(got, "a.go") {
		t.Error("seed a.go should not appear in expanded results")
	}
	if hasPath(got, "b.go") {
		t.Error("seed b.go should not appear in expanded results even though it's a neighbor of a.go")
	}
	if !hasPath(got, "c.go") {
		t.Error("c.go (neighbor of a.go, not a seed) should appear in expanded results")
	}
}

// ---------------------------------------------------------------------------
// mergeGraphResults tests
// ---------------------------------------------------------------------------

func TestMergeGraphResultsPreservesOriginalScoreOnDedup(t *testing.T) {
	original := []store.SearchResult{
		{FilePath: "a.go", Score: 0.9},
		{FilePath: "b.go", Score: 0.7},
	}
	expanded := []store.SearchResult{
		{FilePath: "b.go", Score: 0.6}, // same path, lower score — original should win
		{FilePath: "c.go", Score: 0.8},
	}

	merged := mergeGraphResults(original, expanded)

	// a.go: original score 0.9
	// b.go: original score 0.7 (expanded 0.6 is discarded)
	// c.go: expanded score 0.8
	// Sorted desc: a.go(0.9) > c.go(0.8) > b.go(0.7)

	if len(merged) != 3 {
		t.Fatalf("len(merged) = %d, want 3", len(merged))
	}
	for _, r := range merged {
		if r.FilePath == "b.go" && r.Score != 0.7 {
			t.Errorf("b.go score = %v, want 0.7 (original preserved)", r.Score)
		}
	}
}

func TestMergeGraphResultsSortedByScoreDescending(t *testing.T) {
	original := []store.SearchResult{
		{FilePath: "a.go", Score: 0.5},
	}
	expanded := []store.SearchResult{
		{FilePath: "b.go", Score: 0.9},
		{FilePath: "c.go", Score: 0.3},
	}

	merged := mergeGraphResults(original, expanded)

	want := []string{"b.go", "a.go", "c.go"}
	if len(merged) != 3 {
		t.Fatalf("len(merged) = %d, want 3", len(merged))
	}
	for i, r := range merged {
		if r.FilePath != want[i] {
			t.Errorf("merged[%d] = %s (score=%v), want %s", i, r.FilePath, r.Score, want[i])
		}
	}
	// Verify descending order
	for i := 1; i < len(merged); i++ {
		if merged[i].Score > merged[i-1].Score {
			t.Errorf("merged[%d].Score=%v > merged[%d].Score=%v (must be descending)", i, merged[i].Score, i-1, merged[i-1].Score)
		}
	}
}

func TestMergeGraphResultsEmptyExpanded(t *testing.T) {
	original := []store.SearchResult{
		{FilePath: "a.go", Score: 0.9},
	}
	merged := mergeGraphResults(original, nil)
	if len(merged) != 1 || merged[0].FilePath != "a.go" {
		t.Errorf("merge with nil expanded = %+v, want original unchanged", merged)
	}

	merged = mergeGraphResults(original, []store.SearchResult{})
	if len(merged) != 1 || merged[0].FilePath != "a.go" {
		t.Errorf("merge with empty expanded = %+v, want original unchanged", merged)
	}
}

// ---------------------------------------------------------------------------
// Graph expansion disabled test (via Search)
// ---------------------------------------------------------------------------

// graphlessStore extends fakeStore with stub graph methods that would panic if
// called — proving expandByGraph is never invoked when Graph=false.
type graphlessStore struct {
	fakeStore
}

func (g *graphlessStore) FetchGraphNeighbors(context.Context, int) (map[string][]string, error) {
	panic("FetchGraphNeighbors must not be called when Graph=false")
}
func (g *graphlessStore) FetchChunksByPath(context.Context, int, string, int, int) ([]store.SearchResult, error) {
	panic("FetchChunksByPath must not be called when Graph=false")
}
func (g *graphlessStore) FetchChunksByDirPrefix(context.Context, int, string, int, int) ([]store.SearchResult, error) {
	panic("FetchChunksByDirPrefix must not be called when Graph=false")
}
func (g *graphlessStore) FetchGraphPathsBFS(context.Context, int, []string, int) (map[string]int, error) {
	panic("FetchGraphPathsBFS must not be called when Graph=false")
}
func (g *graphlessStore) GetProjectCommit(context.Context, int) (string, error) {
	return "", nil
}
func (g *graphlessStore) UpdateProjectCommit(context.Context, int, string) error {
	return nil
}

// graphEnabledStore returns empty graph data so Search with Graph=true does not panic.
type graphEnabledStore struct {
	fakeStore
}

func (g *graphEnabledStore) FetchGraphNeighbors(ctx context.Context, projectID int) (map[string][]string, error) {
	return nil, nil // empty graph — expansion returns nothing
}
func (g *graphEnabledStore) FetchChunksByPath(ctx context.Context, projectID int, filePath string, dims, limit int) ([]store.SearchResult, error) {
	return nil, nil
}
func (g *graphEnabledStore) FetchChunksByDirPrefix(ctx context.Context, projectID int, dirPrefix string, dims, limit int) ([]store.SearchResult, error) {
	return nil, nil
}
func (g *graphEnabledStore) FetchGraphPathsBFS(ctx context.Context, projectID int, seedPaths []string, maxDepth int) (map[string]int, error) {
	return nil, nil
}
func (g *graphEnabledStore) GetProjectCommit(ctx context.Context, projectID int) (string, error) {
	return "", nil
}
func (g *graphEnabledStore) UpdateProjectCommit(ctx context.Context, projectID int, commitSHA string) error {
	return nil
}

func TestSearchGraphDisabledDoesNotCallGraphMethods(t *testing.T) {
	st := &graphlessStore{
		fakeStore: fakeStore{
			project:    &store.Project{ID: 1, Name: "p", Model: "bge-m3"},
			simResults: []store.SearchResult{{FilePath: "a.go", Content: "x", Score: 0.9}},
		},
	}
	svc := NewService(st, &fakeEmbedder{vec: []float32{1, 2, 3}, dims: 3})

	// Graph=false must NOT call FetchGraphNeighbors — graphlessStore panics if it does.
	resp, err := svc.Search(context.Background(), Request{
		Project: "p",
		Query:   "graph expansion test",
		TopK:    5,
		Graph:   false,
	})
	if err != nil {
		t.Fatalf("Search with Graph=false: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].FilePath != "a.go" {
		t.Errorf("results = %+v, want just the original hit", resp.Results)
	}
}

func TestSearchGraphEnabledWithEmptyGraphReturnsOriginalResults(t *testing.T) {
	st := &graphEnabledStore{
		fakeStore: fakeStore{
			project:    &store.Project{ID: 1, Name: "p", Model: "bge-m3"},
			simResults: []store.SearchResult{{FilePath: "a.go", Content: "x", Score: 0.9}},
		},
	}
	svc := NewService(st, &fakeEmbedder{vec: []float32{1, 2, 3}, dims: 3})

	resp, err := svc.Search(context.Background(), Request{
		Project: "p",
		Query:   "graph expansion test",
		TopK:    5,
		Graph:   true,
	})
	if err != nil {
		t.Fatalf("Search with Graph=true (empty graph): %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].FilePath != "a.go" {
		t.Errorf("results = %+v, want just the original hit", resp.Results)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// collectPaths extracts unique file paths from search results, preserving
// the order of first appearance.
func collectPaths(results []store.SearchResult) []string {
	seen := make(map[string]bool)
	var out []string
	for _, r := range results {
		if !seen[r.FilePath] {
			seen[r.FilePath] = true
			out = append(out, r.FilePath)
		}
	}
	return out
}

// hasPath reports whether a path slice contains the target string.
func hasPath(paths []string, target string) bool {
	for _, p := range paths {
		if p == target {
			return true
		}
	}
	return false
}
