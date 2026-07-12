// Package deadcode analyses a project's indexed files to find symbols that
// appear to be unused — dead code candidates. It uses file-level import
// dependencies (file_dependencies) to determine whether a file or package is
// referenced by any other indexed file, then classifies its symbols.
package deadcode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/lgldsilva/semidx/internal/analyzer"
	"github.com/lgldsilva/semidx/internal/store"
)

// Finding is one dead-code candidate.
type Finding struct {
	Symbol     string // "ValidateToken"
	Kind       string // "function", "method", "type"
	File       string // "internal/auth/token.go"
	StartLine  int    // 42
	Confidence string // "confirmed", "likely", "public-api"
}

// Analyze scans all indexed files of a project and returns symbols with no
// incoming import edges — potential dead code. projectPath is the root on disk
// where relative file paths are resolved.
func Analyze(ctx context.Context, projectID int, db store.IndexStore, projectPath string) ([]Finding, error) {
	files, err := db.ListFileHashes(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list file hashes: %w", err)
	}

	graph, err := db.FetchGraphNeighbors(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("fetch dependency graph: %w", err)
	}

	// Build importers index: target_dir -> set(source_files).
	// The graph stores source_file -> [target_dir, ...]; we reverse it so we
	// can quickly answer "does anything import this directory?"
	importers := buildImporters(graph)

	var findings []Finding
	for filePath := range files {
		classifyFileSymbols(filePath, projectPath, importers, &findings)
	}

	return findings, nil
}

// buildImporters reverses the dependency graph (source_file -> [target_dir])
// into a lookup of target_dir -> set(source_files) for fast importer checks.
func buildImporters(graph map[string][]string) map[string]map[string]bool {
	importers := make(map[string]map[string]bool)
	for src, targets := range graph {
		for _, tgt := range targets {
			if importers[tgt] == nil {
				importers[tgt] = make(map[string]bool)
			}
			importers[tgt][src] = true
		}
	}
	return importers
}

// classifyFileSymbols reads one file, extracts symbols, and appends dead-code
// findings for any symbol whose package has no incoming import edges.
func classifyFileSymbols(filePath, projectPath string, importers map[string]map[string]bool, findings *[]Finding) {
	absPath := filepath.Clean(filepath.Join(projectPath, filePath))
	if !isWithinProject(absPath, projectPath) {
		return
	}
	// #nosec G304 -- absPath is safely restricted within the projectRoot
	content, err := os.ReadFile(absPath)
	if err != nil {
		// File may have been deleted or moved since last index — skip.
		return
	}

	syms := analyzer.Symbols(filePath, content)
	if len(syms) == 0 {
		return
	}

	// Check whether this file's directory is imported by any other file.
	dir := filepath.Dir(filePath) + "/"
	hasImporters := len(importers[dir]) > 0

	for _, sym := range syms {
		isExported := isExportedSymbol(sym.Name)
		if f := classify(sym, filePath, hasImporters, isExported); f != nil {
			*findings = append(*findings, *f)
		}
	}
}

// isWithinProject reports whether absPath falls inside projectPath. A
// projectPath of "." is treated as unrestricted (relative-index mode).
func isWithinProject(absPath, projectPath string) bool {
	cleanProject := filepath.Clean(projectPath)
	if cleanProject == "." {
		return true
	}
	return strings.HasPrefix(absPath, cleanProject+string(filepath.Separator))
}

// isExportedSymbol reports whether a Go identifier begins with an uppercase
// letter (exported) — safe for empty names.
func isExportedSymbol(name string) bool {
	return len(name) > 0 && unicode.IsUpper(rune(name[0]))
}

// classify returns a Finding for a symbol that appears to be dead, or nil if
// the symbol has sufficient evidence of being in use.
func classify(sym analyzer.Symbol, filePath string, hasImporters, isExported bool) *Finding {
	switch {
	case isExported && !hasImporters:
		// Exported but nothing imports the package — likely dead despite being
		// public (e.g. an unfinished refactor or a package-level entry point).
		return &Finding{
			Symbol:     sym.Name,
			Kind:       sym.Kind,
			File:       filePath,
			StartLine:  sym.StartLine,
			Confidence: "public-api",
		}
	case !isExported && !hasImporters:
		// Unexported and nothing imports the package — clearly dead.
		return &Finding{
			Symbol:     sym.Name,
			Kind:       sym.Kind,
			File:       filePath,
			StartLine:  sym.StartLine,
			Confidence: "confirmed",
		}
	default:
		// The file is imported by at least one other file, so the symbol may or
		// may not be used. We conservatively skip it (no symbol-level call graph).
		return nil
	}
}

// Stats aggregates dead-code findings for reporting.
type Stats struct {
	TotalFindings int
	TotalLines    int
	Confirmed     int
	PublicAPI     int
}

// AggregateStats computes summary statistics from findings.
func AggregateStats(findings []Finding) Stats {
	var s Stats
	s.TotalFindings = len(findings)
	for _, f := range findings {
		switch f.Confidence {
		case "confirmed":
			s.Confirmed++
		case "public-api":
			s.PublicAPI++
		}
		s.TotalLines++
	}
	return s
}
