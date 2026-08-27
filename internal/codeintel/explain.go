package codeintel

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lgldsilva/semidx/internal/analyzer"
	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/imports"
	"github.com/lgldsilva/semidx/internal/store"
)

// ExplainResult contains detailed information about a symbol.
type ExplainResult struct {
	Display   string
	File      string
	Symbol    *analyzer.Symbol
	Imports   []string
	Importers []string
	Tests     []string
}

// Explain gathers detailed information about a symbol at the given file:line.
func Explain(ctx context.Context, db store.IndexStore, proj *store.Project, fl FileLine) (*ExplainResult, error) {
	content, targetSym, err := loadSymbol(proj, fl)
	if err != nil {
		return nil, err
	}
	root := proj.Path

	modulePath := detectModulePath(root)
	fileImports := imports.AnalyzeProject(root, fl.File, content, modulePath)
	sort.Strings(fileImports)

	graph, err := db.FetchGraphNeighbors(ctx, proj.ID)
	if err != nil {
		return nil, fmt.Errorf("fetch dependency graph: %w", err)
	}

	importers := findImportersInGraph(graph, fl.File)
	sort.Strings(importers)

	testFiles := findTestFiles(root, fl.File, targetSym.Name)

	displayName := targetSym.Name
	if pkg := goPackageName(content); pkg != "" {
		displayName = pkg + "." + targetSym.Name
	}

	return &ExplainResult{
		Display:   displayName,
		File:      fl.File,
		Symbol:    targetSym,
		Imports:   fileImports,
		Importers: importers,
		Tests:     testFiles,
	}, nil
}

// findImportersInGraph finds all files that import the given file.
func findImportersInGraph(graph map[string][]string, file string) []string {
	return findDirectCallersForDirs(graph, DependencyDirsForFile(file))
}

// goPackageName extracts the package name from a Go source file.
func goPackageName(content []byte) string {
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "package "))
		}
	}
	return ""
}

// detectModulePath tries to read go.mod from project root to get the module path.
func detectModulePath(root string) string {
	gm := filepath.Clean(filepath.Join(root, "go.mod"))
	// #nosec G304 -- gm points to the project go.mod file, which is safe
	data, err := os.ReadFile(gm)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

// FindTestFiles scans the project for supported test files that reference the
// symbol. Tests commonly live in a top-level tests/ tree rather than beside
// the source file, so a same-directory heuristic misses the most useful
// relationships.
func FindTestFiles(root, filePath, symbolName string) []string {
	if filePath == "" || filepath.IsAbs(filePath) || !filepath.IsLocal(filepath.Clean(filePath)) {
		return nil
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	targetAbs := filepath.Join(rootAbs, filepath.Clean(filePath))
	targetRel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil || !filepath.IsLocal(targetRel) {
		return nil
	}
	targetRel = filepath.ToSlash(filepath.Clean(targetRel))

	var result []string
	err = filepath.WalkDir(rootAbs, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if ignoredTestDir(entry.Name()) && path != rootAbs {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(rootAbs, path)
		if err != nil || !filepath.IsLocal(rel) {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == targetRel || !isTestFile(rel) {
			return nil
		}
		// Open through the root-scoped API so a concurrent symlink change cannot
		// redirect the read outside the project.
		file, err := os.OpenInRoot(rootAbs, filepath.FromSlash(rel))
		if err != nil {
			return nil
		}
		data, err := io.ReadAll(file)
		_ = file.Close()
		if err != nil {
			return nil
		}
		if strings.Contains(string(data), symbolName) {
			result = append(result, rel)
		}
		return nil
	})
	if err != nil {
		return nil
	}
	sort.Strings(result)
	return result
}

func findTestFiles(root, filePath, symbolName string) []string {
	return FindTestFiles(root, filePath, symbolName)
}

func ignoredTestDir(name string) bool {
	return chunker.IsIgnoredDir(name) || name == ".mypy_cache" || name == ".pytest_cache" || name == ".tox"
}

func isTestFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	switch strings.ToLower(filepath.Ext(base)) {
	case ".go":
		return strings.HasSuffix(base, "_test.go")
	case ".py":
		return strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py")
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx":
		if strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") {
			return true
		}
		return filepath.Base(filepath.Dir(path)) == "__tests__"
	default:
		return false
	}
}
