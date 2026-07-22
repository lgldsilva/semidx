package codeintel

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

// fakeStoreDeadCode implements store.IndexStore minimally for deadcode tests.
type fakeStoreDeadCode struct {
	store.IndexStore
	hashes  map[string]string
	graph   map[string][]string
	project *store.Project
}

func (f *fakeStoreDeadCode) ListFileHashes(_ context.Context, _ int) (map[string]string, error) {
	return f.hashes, nil
}

func (f *fakeStoreDeadCode) FetchGraphNeighbors(_ context.Context, _ int) (map[string][]string, error) {
	return f.graph, nil
}

func (f *fakeStoreDeadCode) GetProjectByID(_ context.Context, _ int) (*store.Project, error) {
	return f.project, nil
}

func (f *fakeStoreDeadCode) GetProjectByIdentity(_ context.Context, _ string) (*store.Project, error) {
	return f.project, nil
}

func (f *fakeStoreDeadCode) ListProjects(_ context.Context, _, _ int) ([]store.Project, error) {
	if f.project == nil {
		return []store.Project{}, nil
	}
	return []store.Project{*f.project}, nil
}

func TestDeadCode_WithDeadSymbols(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file with an unexported function (no importers)
	oldFile := filepath.Join(tmpDir, "internal", "old.go")
	if err := os.MkdirAll(filepath.Dir(oldFile), 0o755); err != nil {
		t.Fatal(err)
	}
	oldContent := `package old

func parseV1() {
	println("old")
}
`
	if err := os.WriteFile(oldFile, []byte(oldContent), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create a public API file with exported function (no importers)
	pubFile := filepath.Join(tmpDir, "pkg", "public.go")
	if err := os.MkdirAll(filepath.Dir(pubFile), 0o755); err != nil {
		t.Fatal(err)
	}
	pubContent := `package public

func NewClient() {
	println("client")
}
`
	if err := os.WriteFile(pubFile, []byte(pubContent), 0o600); err != nil {
		t.Fatal(err)
	}

	proj := &store.Project{
		ID:   1,
		Name: "test",
		Path: tmpDir,
	}

	db := &fakeStoreDeadCode{
		project: proj,
		hashes: map[string]string{
			"internal/old.go": "hash1",
			"pkg/public.go":   "hash2",
		},
		graph: map[string][]string{
			// No importers for either package
		},
	}

	ctx := context.Background()

	result, err := DeadCode(ctx, db, proj)
	if err != nil {
		t.Fatalf("DeadCode() error = %v", err)
	}

	if result == nil {
		t.Fatal("DeadCode() returned nil result")
	}

	if len(result.Findings) == 0 {
		t.Error("DeadCode() Findings should not be empty")
	}

	if result.Stats.TotalFindings != len(result.Findings) {
		t.Errorf("DeadCode() Stats.TotalFindings = %d, want %d", result.Stats.TotalFindings, len(result.Findings))
	}
}

func TestDeadCode_EmptyProject(t *testing.T) {
	tmpDir := t.TempDir()

	proj := &store.Project{
		ID:   1,
		Name: "test",
		Path: tmpDir,
	}

	db := &fakeStoreDeadCode{
		project: proj,
		hashes:  map[string]string{},
		graph:   map[string][]string{},
	}

	ctx := context.Background()

	result, err := DeadCode(ctx, db, proj)
	if err != nil {
		t.Fatalf("DeadCode() error = %v", err)
	}

	if len(result.Findings) != 0 {
		t.Errorf("DeadCode() on empty project should have no findings, got %d", len(result.Findings))
	}
}
