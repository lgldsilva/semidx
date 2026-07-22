package codeintel

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/gitenv"
)

// TestGetChangedFilesAndFileAtRef is a regression test for the '--' placement
// bug: putting the revision range after '--' made git treat it as a pathspec,
// so getChangedFiles always returned nothing and getFileAtRef read nothing.
func TestGetChangedFilesAndFileAtRef(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(gitenv.Clean(os.Environ()),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	// --no-verify: the user's global commit-msg hook would otherwise reject
	// these throwaway messages as non-Conventional-Commits.
	git("commit", "-q", "--no-verify", "-m", "c1")
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "--no-verify", "-m", "c2")

	// Pass the temp repo dir explicitly. The test must NOT os.Chdir into it:
	// that mutates the whole process working directory and, combined with git
	// commands elsewhere, has corrupted the real worktree's branch.
	files, err := getChangedFiles(dir, "HEAD~1", "HEAD", false)
	if err != nil {
		t.Fatalf("getChangedFiles: %v", err)
	}
	found := false
	for _, f := range files {
		if f == "b.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected b.txt among changed files, got %v", files)
	}

	content, err := getFileAtRef(dir, "a.txt", "HEAD")
	if err != nil {
		t.Fatalf("getFileAtRef: %v", err)
	}
	if content != "first\n" {
		t.Fatalf("getFileAtRef = %q, want %q", content, "first\n")
	}
}

func TestParseRefRange(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantRef1  string
		wantRef2  string
		wantThree bool
		wantErr   bool
	}{
		{
			name:      "two dot",
			input:     "main..feature",
			wantRef1:  "main",
			wantRef2:  "feature",
			wantThree: false,
		},
		{
			name:      "three dot",
			input:     "main...feature",
			wantRef1:  "main",
			wantRef2:  "feature",
			wantThree: true,
		},
		{
			name:    "no dots",
			input:   "mainfeature",
			wantErr: true,
		},
		{
			name:    "empty first ref (two dot)",
			input:   "..feature",
			wantErr: true,
		},
		{
			name:    "empty second ref (two dot)",
			input:   "main..",
			wantErr: true,
		},
		{
			name:    "empty first ref (three dot)",
			input:   "...feature",
			wantErr: true,
		},
		{
			name:    "empty second ref (three dot)",
			input:   "main...",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref1, ref2, threeDot, err := ParseRefRange(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseRefRange() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if ref1 != tt.wantRef1 || ref2 != tt.wantRef2 || threeDot != tt.wantThree {
					t.Errorf("ParseRefRange() = (%q, %q, %v), want (%q, %q, %v)",
						ref1, ref2, threeDot, tt.wantRef1, tt.wantRef2, tt.wantThree)
				}
			}
		})
	}
}

func TestExtractSymbols(t *testing.T) {
	content := `package main

import "fmt"

// Comment
func HelloWorld() {
	fmt.Println("hello")
}

type Person struct {
	Name string
}

const MaxRetries = 3

var globalConfig string
`

	symbols := extractSymbols("test.go", content)

	expected := map[string]string{
		"func:HelloWorld":  "func",
		"type:Person":      "type",
		"const:MaxRetries": "const",
		"var:globalConfig": "var",
	}

	if len(symbols) != len(expected) {
		t.Fatalf("extractSymbols() found %d symbols, want %d", len(symbols), len(expected))
	}

	for key, kind := range expected {
		sym, ok := symbols[key]
		if !ok {
			t.Errorf("extractSymbols() missing symbol %q", key)
			continue
		}
		if sym.Kind != kind {
			t.Errorf("symbol %q has kind %q, want %q", key, sym.Kind, kind)
		}
	}
}

func TestExtractSymbols_Empty(t *testing.T) {
	symbols := extractSymbols("test.go", "")
	if len(symbols) != 0 {
		t.Errorf("extractSymbols(empty) = %d symbols, want 0", len(symbols))
	}
}

func TestExtractSymbols_OnlyComments(t *testing.T) {
	content := `// This is a comment
/* This is another comment */
// More comments
`
	symbols := extractSymbols("test.go", content)
	if len(symbols) != 0 {
		t.Errorf("extractSymbols(comments only) = %d symbols, want 0", len(symbols))
	}
}

func TestSafeGitRef(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		want bool
	}{
		{"simple branch", "main", true},
		{"branch with slash", "feature/auth", true},
		{"tag", "v1.2.3", true},
		{"head ref", "HEAD~1", true},
		{"starts with dash", "-main", false},
		{"empty", "", false},
		{"with semicolon", "main;rm -rf", false},
		{"with pipe", "main|ls", false},
		{"with ampersand", "main&whoami", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := safeGitRef(tt.ref)
			if got != tt.want {
				t.Errorf("safeGitRef(%q) = %v, want %v", tt.ref, got, tt.want)
			}
		})
	}
}

func TestSafeGitFilepath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"simple file", "main.go", true},
		{"path with directory", "internal/auth/token.go", true},
		{"starts with dash", "-file.go", false},
		{"contains colon", "some:file.go", false},
		{"contains dot-dot", "../etc/passwd", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := safeGitFilepath(tt.path)
			if got != tt.want {
				t.Errorf("safeGitFilepath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestCollectDiffSymbols(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(gitenv.Clean(os.Environ()),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	git("init", "-q")

	// Initial commit with one function
	v1Content := `package main

func Existing() {
	println("exists")
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(v1Content), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "--no-verify", "-m", "v1")

	// Second commit: add a new function, remove existing, change signature of another
	v2Content := `package main

func NewFunc() {
	println("new")
}

func Existing(param string) {
	println("changed signature")
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(v2Content), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "--no-verify", "-m", "v2")

	changedFiles := []string{"main.go"}
	newSyms, _, changedSyms := collectDiffSymbols(dir, changedFiles, "HEAD~1", "HEAD")

	// NewFunc should be new
	foundNew := false
	for _, s := range newSyms {
		if s.Name == "NewFunc" {
			foundNew = true
		}
	}
	if !foundNew {
		t.Errorf("expected NewFunc in new symbols, got %v", newSyms)
	}

	// Existing should be changed (signature changed)
	foundChanged := false
	for _, s := range changedSyms {
		if s.Name == "Existing" {
			foundChanged = true
		}
	}
	if !foundChanged {
		t.Errorf("expected Existing in changed symbols, got %v", changedSyms)
	}
}

func TestSortDiffSymbols(t *testing.T) {
	newSyms := []SymbolDiff{
		{Name: "B", FilePath: "b.go", Line: 10},
		{Name: "A", FilePath: "a.go", Line: 5},
		{Name: "C", FilePath: "a.go", Line: 3},
	}
	removedSyms := []SymbolDiff{
		{Name: "Z", FilePath: "z.go", Line: 1},
		{Name: "X", FilePath: "a.go", Line: 100},
	}
	changedSyms := []SymbolDiff{
		{Name: "Y", FilePath: "y.go", Line: 50},
	}

	sortDiffSymbols(&newSyms, &removedSyms, &changedSyms)

	// Should be sorted by file path, then by line
	if newSyms[0].Name != "C" || newSyms[1].Name != "A" || newSyms[2].Name != "B" {
		t.Errorf("sortDiffSymbols newSyms wrong order: %v", newSyms)
	}
	if removedSyms[0].Name != "X" || removedSyms[1].Name != "Z" {
		t.Errorf("sortDiffSymbols removedSyms wrong order: %v", removedSyms)
	}
	if len(changedSyms) != 1 || changedSyms[0].Name != "Y" {
		t.Errorf("sortDiffSymbols changedSyms wrong order: %v", changedSyms)
	}
}
