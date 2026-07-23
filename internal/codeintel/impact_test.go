package codeintel

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

type fakeStoreImpact struct {
	store.IndexStore
	graph   map[string][]string
	project *store.Project
	err     error
}

func (f *fakeStoreImpact) FetchGraphNeighbors(_ context.Context, _ int) (map[string][]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.graph, nil
}

func TestImpact_MultiLevelDepth(t *testing.T) {
	tmpDir := t.TempDir()
	// Seed package lives at internal/auth/
	authDir := filepath.Join(tmpDir, "internal", "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(authDir, "token.go")
	content := `package auth

func ValidateToken() {
	println("ok")
}
`
	if err := os.WriteFile(testFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	proj := &store.Project{ID: 1, Name: "test", Path: tmpDir}
	// 3-level reverse chain:
	//   depth1: internal/api/api.go, cmd/main.go  import internal/auth/
	//   depth2: internal/web/web.go               imports internal/api/
	//   depth3: deploy/hook.go                    imports internal/web/
	db := &fakeStoreImpact{
		project: proj,
		graph: map[string][]string{
			"internal/api/api.go":    {"internal/auth/"},
			"cmd/main.go":            {"internal/auth/", "internal/web/"},
			"internal/web/web.go":    {"internal/api/"},
			"deploy/hook.go":         {"internal/web/"},
			"internal/auth/token.go": {},
		},
	}

	ctx := context.Background()
	fl := FileLine{File: "internal/auth/token.go", Line: 3}

	// depth=1 → only direct importers
	r1, err := Impact(ctx, db, proj, fl, 1)
	if err != nil {
		t.Fatalf("Impact(depth=1) error = %v", err)
	}
	if r1.Symbol == nil || r1.Symbol.Name != "ValidateToken" {
		t.Fatalf("Impact symbol = %v, want ValidateToken", r1.Symbol)
	}
	if r1.TotalCount != 2 {
		t.Fatalf("Impact(depth=1) TotalCount = %d, want 2; affected=%v", r1.TotalCount, r1.Affected)
	}
	assertImpactFiles(t, r1.Affected, map[string]int{
		"internal/api/api.go": 1,
		"cmd/main.go":         1,
	})

	// depth=2 → adds web.go
	r2, err := Impact(ctx, db, proj, fl, 2)
	if err != nil {
		t.Fatalf("Impact(depth=2) error = %v", err)
	}
	if r2.TotalCount != 3 {
		t.Fatalf("Impact(depth=2) TotalCount = %d, want 3; affected=%v", r2.TotalCount, r2.Affected)
	}
	assertImpactFiles(t, r2.Affected, map[string]int{
		"internal/api/api.go": 1,
		"cmd/main.go":         1,
		"internal/web/web.go": 2,
	})

	// depth=3 (and default) → full chain including deploy/hook.go
	r3, err := Impact(ctx, db, proj, fl, 3)
	if err != nil {
		t.Fatalf("Impact(depth=3) error = %v", err)
	}
	if r3.TotalCount != 4 {
		t.Fatalf("Impact(depth=3) TotalCount = %d, want 4; affected=%v", r3.TotalCount, r3.Affected)
	}
	assertImpactFiles(t, r3.Affected, map[string]int{
		"internal/api/api.go": 1,
		"cmd/main.go":         1,
		"internal/web/web.go": 2,
		"deploy/hook.go":      3,
	})

	// Sorted by depth then path: depth-1 paths alphabetical first.
	if r3.Affected[0].File != "cmd/main.go" || r3.Affected[1].File != "internal/api/api.go" {
		t.Errorf("Impact sort order wrong: got %v", r3.Affected)
	}
}

func TestImpact_EmptyGraph(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "lonely.go")
	if err := os.WriteFile(testFile, []byte("package main\nfunc Alone() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	proj := &store.Project{ID: 1, Path: tmpDir}
	db := &fakeStoreImpact{project: proj, graph: map[string][]string{}}

	r, err := Impact(context.Background(), db, proj, FileLine{File: "lonely.go", Line: 2}, 5)
	if err != nil {
		t.Fatalf("Impact() error = %v", err)
	}
	if r.TotalCount != 0 || len(r.Affected) != 0 {
		t.Errorf("Impact(empty graph) = %+v, want empty", r)
	}
}

func TestImpact_FileNotFound(t *testing.T) {
	proj := &store.Project{ID: 1, Path: "/nonexistent"}
	db := &fakeStoreImpact{project: proj, graph: map[string][]string{}}
	_, err := Impact(context.Background(), db, proj, FileLine{File: "missing.go", Line: 1}, 5)
	if err == nil {
		t.Error("Impact() with missing file should error")
	}
}

func TestImpact_GraphError(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main\nfunc F() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	proj := &store.Project{ID: 1, Path: tmpDir}
	db := &fakeStoreImpact{project: proj, err: context.DeadlineExceeded}
	_, err := Impact(context.Background(), db, proj, FileLine{File: "main.go", Line: 2}, 5)
	if err == nil {
		t.Error("Impact() with graph error should error")
	}
}

func TestImpact_PathEscape(t *testing.T) {
	tmpDir := t.TempDir()
	proj := &store.Project{ID: 1, Path: tmpDir}
	db := &fakeStoreImpact{project: proj, graph: map[string][]string{}}
	_, err := Impact(context.Background(), db, proj, FileLine{File: "../outside.go", Line: 1}, 5)
	if err == nil {
		t.Error("Impact() with path escape should error")
	}
}

func TestImpact_NoSymbols(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "empty.go")
	// No function/type declarations — analyzer finds nothing.
	if err := os.WriteFile(testFile, []byte("package main\n// just a comment\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	proj := &store.Project{ID: 1, Path: tmpDir}
	db := &fakeStoreImpact{project: proj, graph: map[string][]string{}}
	_, err := Impact(context.Background(), db, proj, FileLine{File: "empty.go", Line: 1}, 5)
	if err == nil {
		t.Error("Impact() with no symbols should error")
	}
}

func TestClampImpactDepth(t *testing.T) {
	tests := []struct {
		in, want int
	}{
		{0, defaultImpactDepth},
		{-1, defaultImpactDepth},
		{3, 3},
		{10, 10},
		{11, maxImpactDepth},
		{100, maxImpactDepth},
	}
	for _, tt := range tests {
		if got := clampImpactDepth(tt.in); got != tt.want {
			t.Errorf("clampImpactDepth(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestReverseDependencyClosure_Dedup(t *testing.T) {
	// Same file reachable via two paths at different depths — keep shallowest.
	graph := map[string][]string{
		"a/a.go": {"seed/"},
		"b/b.go": {"a/"},
		"c/c.go": {"seed/", "b/"}, // also direct importer of seed
	}
	got := reverseDependencyClosure(graph, "seed/", "seed/x.go", 5)
	byFile := map[string]int{}
	for _, n := range got {
		byFile[n.File] = n.Depth
	}
	if byFile["c/c.go"] != 1 {
		t.Errorf("c/c.go depth = %d, want 1 (shallowest)", byFile["c/c.go"])
	}
	if byFile["a/a.go"] != 1 {
		t.Errorf("a/a.go depth = %d, want 1", byFile["a/a.go"])
	}
	if byFile["b/b.go"] != 2 {
		t.Errorf("b/b.go depth = %d, want 2", byFile["b/b.go"])
	}
}

func assertImpactFiles(t *testing.T, nodes []ImpactNode, want map[string]int) {
	t.Helper()
	got := make(map[string]int, len(nodes))
	for _, n := range nodes {
		got[n.File] = n.Depth
	}
	for file, depth := range want {
		if got[file] != depth {
			t.Errorf("file %q depth = %d, want %d (got map %v)", file, got[file], depth, got)
		}
	}
	for file := range got {
		if _, ok := want[file]; !ok {
			t.Errorf("unexpected affected file %q", file)
		}
	}
}
