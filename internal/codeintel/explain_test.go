package codeintel

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

// fakeStoreExplain implements store.IndexStore minimally for explain tests.
type fakeStoreExplain struct {
	store.IndexStore
	graph   map[string][]string
	project *store.Project
}

func (f *fakeStoreExplain) FetchGraphNeighbors(_ context.Context, _ int) (map[string][]string, error) {
	return f.graph, nil
}

func (f *fakeStoreExplain) GetProjectByIdentity(_ context.Context, _ string) (*store.Project, error) {
	return f.project, nil
}

func (f *fakeStoreExplain) ListProjects(_ context.Context, _, _ int) ([]store.Project, error) {
	if f.project == nil {
		return []store.Project{}, nil
	}
	return []store.Project{*f.project}, nil
}

func TestExplain_WithImporters(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "auth", "token.go")
	if err := os.MkdirAll(filepath.Dir(testFile), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `package auth

func ValidateToken() {
	println("validating")
}
`
	if err := os.WriteFile(testFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create a test file that references the symbol
	testFileTest := filepath.Join(tmpDir, "auth", "token_test.go")
	testContent := `package auth

import "testing"

func TestValidateToken(t *testing.T) {
	ValidateToken()
}
`
	if err := os.WriteFile(testFileTest, []byte(testContent), 0o600); err != nil {
		t.Fatal(err)
	}

	proj := &store.Project{
		ID:   1,
		Name: "test",
		Path: tmpDir,
	}

	db := &fakeStoreExplain{
		project: proj,
		graph: map[string][]string{
			"cmd/main.go":    {"auth/"},
			"api/handler.go": {"auth/"},
		},
	}

	ctx := context.Background()
	fl := FileLine{File: "auth/token.go", Line: 3}

	result, err := Explain(ctx, db, proj, fl)
	if err != nil {
		t.Fatalf("Explain() error = %v", err)
	}

	if result.Symbol == nil {
		t.Fatal("Explain() returned nil symbol")
	}
	if result.Symbol.Name != "ValidateToken" {
		t.Errorf("Explain() symbol name = %q, want ValidateToken", result.Symbol.Name)
	}
	if result.Display != "auth.ValidateToken" {
		t.Errorf("Explain() display = %q, want auth.ValidateToken", result.Display)
	}
	if len(result.Importers) != 2 {
		t.Errorf("Explain() Importers = %d items, want 2", len(result.Importers))
	}
	if len(result.Tests) != 1 {
		t.Errorf("Explain() Tests = %d items, want 1", len(result.Tests))
	}
}

func TestExplain_FileNotFound(t *testing.T) {
	proj := &store.Project{
		ID:   1,
		Path: "/nonexistent",
	}

	db := &fakeStoreExplain{
		project: proj,
		graph:   map[string][]string{},
	}

	ctx := context.Background()
	fl := FileLine{File: "missing.go", Line: 1}

	_, err := Explain(ctx, db, proj, fl)
	if err == nil {
		t.Error("Explain() with missing file should error")
	}
}

func TestGoPackageName(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		want    string
	}{
		{
			name:    "simple package",
			content: []byte("package main\n\nfunc main() {}"),
			want:    "main",
		},
		{
			name:    "package with spaces",
			content: []byte("  package   mypackage  \n"),
			want:    "mypackage",
		},
		{
			name:    "no package declaration",
			content: []byte("func main() {}"),
			want:    "",
		},
		{
			name:    "empty content",
			content: []byte(""),
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := goPackageName(tt.content)
			if got != tt.want {
				t.Errorf("goPackageName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectModulePath(t *testing.T) {
	dir := t.TempDir()

	// No go.mod
	got := detectModulePath(dir)
	if got != "" {
		t.Errorf("detectModulePath() with no go.mod = %q, want empty", got)
	}

	// Create go.mod
	gomod := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(gomod, []byte("module github.com/example/myproject\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got = detectModulePath(dir)
	want := "github.com/example/myproject"
	if got != want {
		t.Errorf("detectModulePath() = %q, want %q", got, want)
	}
}

func TestDetectModulePath_MultilineGoMod(t *testing.T) {
	dir := t.TempDir()
	gomod := filepath.Join(dir, "go.mod")
	content := `// Some comment
module github.com/user/project

go 1.26

require (
	example.com/dep v1.0.0
)
`
	if err := os.WriteFile(gomod, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	got := detectModulePath(dir)
	want := "github.com/user/project"
	if got != want {
		t.Errorf("detectModulePath() = %q, want %q", got, want)
	}
}

func TestFindTestFiles(t *testing.T) {
	root := t.TempDir()

	// Create a test structure
	pkgDir := filepath.Join(root, "internal", "auth")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create source file
	srcFile := filepath.Join(pkgDir, "token.go")
	if err := os.WriteFile(srcFile, []byte("package auth\n\nfunc GenerateToken() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create test file that references the symbol
	testFile1 := filepath.Join(pkgDir, "token_test.go")
	if err := os.WriteFile(testFile1, []byte("package auth\n\nfunc TestGenerateToken() { GenerateToken() }\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create test file that doesn't reference the symbol
	testFile2 := filepath.Join(pkgDir, "other_test.go")
	if err := os.WriteFile(testFile2, []byte("package auth\n\nfunc TestOther() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create non-test file
	otherFile := filepath.Join(pkgDir, "other.go")
	if err := os.WriteFile(otherFile, []byte("package auth\n\nfunc Other() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result := findTestFiles(root, "internal/auth/token.go", "GenerateToken")

	if len(result) != 1 {
		t.Fatalf("findTestFiles() returned %d files, want 1", len(result))
	}

	if result[0] != "internal/auth/token_test.go" {
		t.Errorf("findTestFiles() = %v, want [internal/auth/token_test.go]", result)
	}
}

func TestFindTestFiles_NoDir(t *testing.T) {
	result := findTestFiles("/nonexistent/path", "some/file.go", "Symbol")
	if result != nil {
		t.Errorf("findTestFiles() on nonexistent dir = %v, want nil", result)
	}
}

func TestFindTestFiles_MultipleLanguages(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "src")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create files
	srcFile := filepath.Join(pkgDir, "main.go")
	if err := os.WriteFile(srcFile, []byte("package main\n\nfunc MyFunc() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Go test
	goTest := filepath.Join(pkgDir, "main_test.go")
	if err := os.WriteFile(goTest, []byte("package main\n\nfunc TestMyFunc() { MyFunc() }\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Python test
	pyTest := filepath.Join(pkgDir, "test_main_test.py")
	if err := os.WriteFile(pyTest, []byte("def test_my_func():\n    MyFunc()\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// JS test
	jsTest := filepath.Join(pkgDir, "main.test.js")
	if err := os.WriteFile(jsTest, []byte("test('MyFunc', () => { MyFunc(); });\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result := findTestFiles(root, "src/main.go", "MyFunc")

	if len(result) != 3 {
		t.Fatalf("findTestFiles() returned %d files, want 3: %v", len(result), result)
	}
}

func TestFindImportersInGraph(t *testing.T) {
	graph := map[string][]string{
		"cmd/main.go":         {"internal/auth/"},
		"internal/api/api.go": {"internal/auth/"},
	}

	result := findImportersInGraph(graph, "internal/auth/token.go")

	if len(result) != 2 {
		t.Fatalf("findImportersInGraph() returned %d items, want 2", len(result))
	}

	found := make(map[string]bool)
	for _, r := range result {
		found[r] = true
	}
	if !found["cmd/main.go"] || !found["internal/api/api.go"] {
		t.Errorf("findImportersInGraph() = %v, want both files", result)
	}
}

func TestDetectModulePath_ErrorPath(t *testing.T) {
	// Non-existent directory
	got := detectModulePath("/nonexistent/path")
	if got != "" {
		t.Errorf("detectModulePath() on non-existent dir = %q, want empty", got)
	}
}

func TestFindTestFiles_ErrorPath(t *testing.T) {
	// Non-existent directory
	result := findTestFiles("/nonexistent", "some/file.go", "Symbol")
	if result != nil {
		t.Errorf("findTestFiles() on non-existent dir = %v, want nil", result)
	}
}

func TestFindTestFiles_ReadError(t *testing.T) {
	tmpDir := t.TempDir()
	pkgDir := filepath.Join(tmpDir, "pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a test file
	testFile := filepath.Join(pkgDir, "test_test.go")
	if err := os.WriteFile(testFile, []byte("package pkg"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Make it unreadable (Unix only)
	if err := os.Chmod(testFile, 0o000); err != nil {
		t.Skip("Cannot change file permissions")
	}
	defer func() { _ = os.Chmod(testFile, 0o600) }()

	// Should handle read error gracefully
	result := findTestFiles(tmpDir, "pkg/main.go", "Symbol")
	// May or may not include the file depending on OS, just ensure no panic
	_ = result
}
