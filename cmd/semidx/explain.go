package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/analyzer"
	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/imports"
	"github.com/lgldsilva/semidx/internal/projectref"
	"github.com/lgldsilva/semidx/internal/store"
)

// newExplainCmd returns the `semidx explain` command, which shows structured
// information about a symbol at a given file:line reference.
func newExplainCmd(d *deps) *cobra.Command {
	var projectArg string
	c := &cobra.Command{
		Use:   "explain <file:line>",
		Short: "Show detailed information about a symbol in context",
		Long: `Show detailed information about a symbol at the given file:line reference:
its location, dependencies, importers, and related test files.

The file path is relative to the project root, matching how the index stores it.

Examples:

  semidx explain internal/auth/token.go:42
  semidx explain pkg/client/client.go:10 --project ./my-repo`,
		Args: cobra.ExactArgs(1),
		Example: `  semidx explain internal/auth/token.go:42
  semidx explain cmd/semidx/main.go:110 --project ./my-repo`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			fl, err := parseFileLine(args[0])
			if err != nil {
				return err
			}

			db, err := d.indexStore(ctx)
			if err != nil {
				return err
			}

			resolved, err := resolveExplainProject(ctx, db, projectArg)
			if err != nil {
				return err
			}

			return printExplain(ctx, db, resolved, fl)
		},
	}
	c.Flags().StringVar(&projectArg, "project", "", "Project path or name (default: the project enclosing the current directory)")
	return c
}

// resolveExplainProject resolves the project for the explain command.
func resolveExplainProject(ctx context.Context, db store.IndexStore, projectArg string) (*store.Project, error) {
	if projectArg != "" {
		return projectref.Resolve(ctx, db, projectArg)
	}
	if gi := gitmeta.Resolve(ctx, "."); gi.IsGit {
		if p, err := db.GetProjectByIdentity(ctx, gi.Identity); err == nil {
			return p, nil
		}
	}
	projects, err := db.ListProjects(ctx, 0, 0)
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if p := projectref.Enclosing(cwd, projects); p != nil {
		return p, nil
	}
	return nil, fmt.Errorf("no indexed project found — run 'semidx index --project .' first")
}

// explainInfo is the structured information about one symbol.
// printExplain gathers and displays detailed info about a symbol.
func printExplain(ctx context.Context, db store.IndexStore, proj *store.Project, fl fileLineArg) error {
	root := proj.Path
	if root == "" {
		root = "."
	}
	absPath := filepath.Clean(filepath.Join(root, fl.File))
	if !strings.HasPrefix(absPath, filepath.Clean(root)+string(filepath.Separator)) && absPath != filepath.Clean(root) && root != "." {
		return fmt.Errorf("path %q escapes project root", fl.File)
	}
	// #nosec G304 -- absPath is safely restricted within the project root
	content, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", fl.File, err)
	}

	// Extract symbols via tree-sitter.
	syms := analyzer.Symbols(fl.File, content)
	if len(syms) == 0 {
		return fmt.Errorf("no symbols found in %s", fl.File)
	}

	// Find the symbol at the given line.
	var targetSym *analyzer.Symbol
	for _, s := range syms {
		if fl.Line >= s.StartLine && fl.Line <= s.EndLine {
			targetSym = &s
			break
		}
	}
	if targetSym == nil {
		return fmt.Errorf("no symbol found at %s:%d", fl.File, fl.Line)
	}

	// Extract file-level imports (for "Dependencies" section).
	// We discover the module path by looking at go.mod if this is a Go project.
	modulePath := detectModulePath(root)
	fileImports := imports.Analyze(fl.File, content, modulePath)

	// Get the dependency graph to find importers.
	graph, err := db.FetchGraphNeighbors(ctx, proj.ID)
	if err != nil {
		return fmt.Errorf("fetch dependency graph: %w", err)
	}

	fileDir := filepath.Dir(fl.File) + "/"
	var importers []string
	importerMap := make(map[string]bool)
	for src, targets := range graph {
		for _, tgt := range targets {
			if tgt == fileDir {
				if !importerMap[src] {
					importerMap[src] = true
					importers = append(importers, src)
				}
				break
			}
		}
	}
	sort.Strings(importers)

	// Find test files referencing this symbol.
	testFiles := findTestFiles(root, fl.File, targetSym.Name)

	// Build display name: for Go files, prefix the package name.
	displayName := targetSym.Name
	if pkg := goPackageName(content); pkg != "" {
		displayName = pkg + "." + targetSym.Name
	}

	// Print the output.
	fmt.Println()
	fmt.Printf("  %s — %s (%s:%d-%d)\n", displayName, targetSym.Kind, fl.File, targetSym.StartLine, targetSym.EndLine)
	fmt.Println("  " + strings.Repeat("─", 80))

	// Dependencies.
	fmt.Printf("\n  Dependencies (%d):\n", len(fileImports))
	if len(fileImports) > 0 {
		sort.Strings(fileImports)
		for _, dep := range fileImports {
			fmt.Printf("    %s\n", dep)
		}
	} else {
		fmt.Println("    (none detected)")
	}

	// Importers.
	fmt.Printf("\n  Imported by (%d files):\n", len(importers))
	if len(importers) > 0 {
		for _, imp := range importers {
			fmt.Printf("    %s\n", imp)
		}
	} else {
		fmt.Println("    (none — this package is not imported by any indexed file)")
	}

	// Tests.
	fmt.Printf("\n  Tests (%d files):\n", len(testFiles))
	if len(testFiles) > 0 {
		sort.Strings(testFiles)
		for _, tf := range testFiles {
			fmt.Printf("    %s\n", tf)
		}
	} else {
		fmt.Println("    (none found)")
	}

	return nil
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
	return result
}
