package indexing

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadModulePathWithValidGoMod(t *testing.T) {
	dir := t.TempDir()
	gomod := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(gomod, []byte("module github.com/lgldsilva/semidx\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := ReadModulePath(dir)
	want := "github.com/lgldsilva/semidx"
	if got != want {
		t.Errorf("ReadModulePath = %q, want %q", got, want)
	}
}

func TestReadModulePathNoGoMod(t *testing.T) {
	dir := t.TempDir()
	got := ReadModulePath(dir)
	if got != "" {
		t.Errorf("ReadModulePath with no go.mod = %q, want empty string", got)
	}
}

func TestReadModulePathMalformedGoMod(t *testing.T) {
	dir := t.TempDir()
	gomod := filepath.Join(dir, "go.mod")
	// go.mod without a module directive
	if err := os.WriteFile(gomod, []byte("go 1.25\n\nrequire (\n\tfmt v1.0.0\n)\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := ReadModulePath(dir)
	if got != "" {
		t.Errorf("ReadModulePath with no module directive = %q, want empty string", got)
	}
}

func TestReadModulePathEmptyGoMod(t *testing.T) {
	dir := t.TempDir()
	gomod := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(gomod, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	got := ReadModulePath(dir)
	if got != "" {
		t.Errorf("ReadModulePath with empty go.mod = %q, want empty string", got)
	}
}

func TestReadModulePathTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	gomod := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(gomod, []byte("module example.com/my/module\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := ReadModulePath(dir)
	want := "example.com/my/module"
	if got != want {
		t.Errorf("ReadModulePath = %q, want %q", got, want)
	}
}
