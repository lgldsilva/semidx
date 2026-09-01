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

func TestFindModulePath(t *testing.T) {
	t.Parallel()

	t.Run("root go.mod", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeGoMod(t, dir, "go.mod", "module example.com/root\n")
		got := FindModulePath(dir, "main.go")
		want := "example.com/root"
		if got != want {
			t.Errorf("FindModulePath = %q, want %q", got, want)
		}
	})

	t.Run("nested go.mod", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeGoMod(t, dir, "go.mod", "module example.com/root\n")
		writeGoMod(t, dir, "service-a/go.mod", "module example.com/service-a\n")
		writeGoMod(t, dir, "service-a/internal/lib/lib.go", "package lib\n")

		got := FindModulePath(dir, "service-a/internal/lib/lib.go")
		want := "example.com/service-a"
		if got != want {
			t.Errorf("FindModulePath = %q, want %q", got, want)
		}
	})

	t.Run("falls back to root", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeGoMod(t, dir, "go.mod", "module example.com/root\n")
		writeGoMod(t, dir, "cmd/tool/main.go", "package main\n")

		got := FindModulePath(dir, "cmd/tool/main.go")
		want := "example.com/root"
		if got != want {
			t.Errorf("FindModulePath = %q, want %q", got, want)
		}
	})

	t.Run("no go.mod", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeGoMod(t, dir, "main.go", "package main\n")
		got := FindModulePath(dir, "main.go")
		if got != "" {
			t.Errorf("FindModulePath = %q, want empty string", got)
		}
	})
}

func writeGoMod(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
