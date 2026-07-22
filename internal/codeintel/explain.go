package codeintel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lgldsilva/semidx/internal/analyzer"
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
	fileImports := imports.Analyze(fl.File, content, modulePath)
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
	fileDir := filepath.Dir(file) + "/"
	return findDirectCallers(graph, fileDir)
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

// findTestFiles looks for test files in the same directory as the given file
// that reference the given symbol name.
func findTestFiles(root, filePath, symbolName string) []string {
	dir := filepath.Dir(filepath.Join(root, filePath))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var result []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, "_test.go") && !strings.HasSuffix(name, "_test.py") && !strings.HasSuffix(name, ".test.js") {
			continue
		}
		relPath := filepath.Join(filepath.Dir(filePath), name)
		testAbsPath := filepath.Clean(filepath.Join(root, relPath))
		if !strings.HasPrefix(testAbsPath, filepath.Clean(root)+string(filepath.Separator)) && testAbsPath != filepath.Clean(root) && root != "." {
			continue
		}
		// #nosec G304 -- reading related test files inside the user's project is safe
		testContent, err := os.ReadFile(testAbsPath)
		if err != nil {
			continue
		}
		if strings.Contains(string(testContent), symbolName) {
			result = append(result, relPath)
		}
	}
	sort.Strings(result)
	return result
}
