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

	tests := []struct {
		name  string
		files map[string]string
		rel   string
		want  string
	}{
		{
			name:  "root go.mod",
			files: map[string]string{"go.mod": "module example.com/root\n"},
			rel:   "main.go",
			want:  "example.com/root",
		},
		{
			name: "nested go.mod",
			files: map[string]string{
				"go.mod":                        "module example.com/root\n",
				"service-a/go.mod":              "module example.com/service-a\n",
				"service-a/internal/lib/lib.go": "package lib\n",
			},
			rel:  "service-a/internal/lib/lib.go",
			want: "example.com/service-a",
		},
		{
			name: "falls back to root",
			files: map[string]string{
				"go.mod":           "module example.com/root\n",
				"cmd/tool/main.go": "package main\n",
			},
			rel:  "cmd/tool/main.go",
			want: "example.com/root",
		},
		{
			name:  "no go.mod",
			files: map[string]string{"main.go": "package main\n"},
			rel:   "main.go",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for rel, content := range tt.files {
				writeGoMod(t, dir, rel, content)
			}
			got := FindModulePath(dir, tt.rel)
			if got != tt.want {
				t.Errorf("FindModulePath = %q, want %q", got, tt.want)
			}
		})
	}
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

// TestModuleInfoForCachesByFileDir ensures the per-directory cache is keyed by
// the file's own directory, so files deeper than the go.mod hit the cache
// instead of re-walking the tree on every unit.
func TestModuleInfoForCachesByFileDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeGoMod(t, root, "go.mod", "module example.com/root\n")
	writeGoMod(t, root, "service-a/go.mod", "module example.com/service-a\n")
	writeGoMod(t, root, "service-a/internal/lib/lib.go", "package lib\n")

	idx := &Indexer{projectPath: root, modulePath: "example.com/root"}
	idx.modulePathCache = make(map[string]modulePathCacheEntry)

	rel := "service-a/internal/lib/lib.go"
	dir, mp := idx.moduleInfoFor(rel)
	if dir != "service-a" || mp != "example.com/service-a" {
		t.Fatalf("moduleInfoFor = (%q, %q), want (%q, %q)", dir, mp, "service-a", "example.com/service-a")
	}

	entry, ok := idx.modulePathCache["service-a/internal/lib"]
	if !ok {
		t.Fatalf("cache not keyed by the file directory: %v", idx.modulePathCache)
	}
	if entry.dir != "service-a" || entry.modulePath != "example.com/service-a" {
		t.Errorf("cached entry = %+v, want {service-a example.com/service-a}", entry)
	}

	// A second lookup must be served from the cache: removing the go.mod from
	// disk cannot change the answer.
	if err := os.Remove(filepath.Join(root, "service-a", "go.mod")); err != nil {
		t.Fatal(err)
	}
	dir, mp = idx.moduleInfoFor(rel)
	if dir != "service-a" || mp != "example.com/service-a" {
		t.Errorf("cached moduleInfoFor = (%q, %q), want (%q, %q)", dir, mp, "service-a", "example.com/service-a")
	}
}
