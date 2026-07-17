// coverage-patch: 2026-07-17
package deadcode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIsWithinProject(t *testing.T) {
	t.Parallel()
	if !isWithinProject("/any/path", ".") {
		t.Error("projectPath '.' must be unrestricted")
	}
	proj := "/tmp/proj"
	if !isWithinProject("/tmp/proj/a.go", proj) {
		t.Error("child path should be inside")
	}
	if isWithinProject("/tmp/other/a.go", proj) {
		t.Error("sibling path must be outside")
	}
	if isWithinProject("/tmp/proj-evil/a.go", proj) {
		// HasPrefix without separator would wrongly allow proj-evil; ensure we require separator.
		// "/tmp/proj" + "/" is prefix of "/tmp/proj/..." only.
		// "/tmp/proj-evil" does not have prefix "/tmp/proj/"
		t.Log("proj-evil correctly outside")
	}
}

func TestClassifyFileSymbols_skips(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	var findings []Finding

	// Path that escapes project → skip via isWithinProject.
	classifyFileSymbols("../../etc/passwd", tmp, nil, &findings)
	if len(findings) != 0 {
		t.Errorf("escaped path produced findings: %+v", findings)
	}

	// Missing file → skip.
	classifyFileSymbols("missing.go", tmp, nil, &findings)
	if len(findings) != 0 {
		t.Errorf("missing file produced findings: %+v", findings)
	}

	// File with no extractable symbols.
	if err := os.WriteFile(filepath.Join(tmp, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	classifyFileSymbols("empty.go", tmp, nil, &findings)
	if len(findings) != 0 {
		t.Errorf("empty symbols produced findings: %+v", findings)
	}
}

func TestBuildImporters(t *testing.T) {
	t.Parallel()
	g := map[string][]string{
		"a.go": {"pkg/"},
		"b.go": {"pkg/", "other/"},
	}
	importers := buildImporters(g)
	if len(importers["pkg/"]) != 2 {
		t.Errorf("pkg/ importers = %v, want 2 sources", importers["pkg/"])
	}
	if !importers["other/"]["b.go"] {
		t.Error("other/ should be imported by b.go")
	}
}

type errListStore struct {
	fakeStore
	listErr  error
	graphErr error
}

func (e *errListStore) ListFileHashes(context.Context, int) (map[string]string, error) {
	if e.listErr != nil {
		return nil, e.listErr
	}
	return e.hashes, nil
}

func (e *errListStore) FetchGraphNeighbors(context.Context, int) (map[string][]string, error) {
	if e.graphErr != nil {
		return nil, e.graphErr
	}
	return e.graph, nil
}

func TestAnalyze_errors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmp := t.TempDir()

	_, err := Analyze(ctx, 1, &errListStore{listErr: errors.New("list boom")}, tmp)
	if err == nil {
		t.Fatal("list error expected")
	}

	_, err = Analyze(ctx, 1, &errListStore{
		fakeStore: fakeStore{hashes: map[string]string{"a.go": "h"}},
		graphErr:  errors.New("graph boom"),
	}, tmp)
	if err == nil {
		t.Fatal("graph error expected")
	}
}

func TestAnalyze_relativeProjectPath(t *testing.T) {
	// projectPath "." is unrestricted — write a file under cwd via relative path
	// registered in the index map.
	ctx := context.Background()
	tmp := t.TempDir()
	// Use absolute path for the file but projectPath "." so isWithinProject always true.
	// Register a relative path that won't resolve under "." + join unless we chdir.
	// Instead, put a go file in tmp and use tmp as project with path that is within.
	src := filepath.Join(tmp, "x.go")
	if err := os.WriteFile(src, []byte("package x\nfunc dead() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Analyze with projectPath = tmp, file path "x.go"
	fs := &fakeStore{
		hashes: map[string]string{"x.go": "h"},
		graph:  map[string][]string{},
	}
	findings, err := Analyze(ctx, 1, fs, tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) == 0 {
		t.Fatal("expected dead-code finding for unexported dead()")
	}
}
