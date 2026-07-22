package codeintel

import (
	"os"
	"path/filepath"
	"testing"
)

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
