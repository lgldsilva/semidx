package codeintel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lgldsilva/semidx/internal/analyzer"
	"github.com/lgldsilva/semidx/internal/store"
)

// CallersResult contains the results of a caller analysis.
type CallersResult struct {
	Symbol     *analyzer.Symbol
	Direct     []string
	Transitive []string
}

// loadSymbol reads the file at fl within proj, parses its symbols, and returns
// the file content plus the symbol at fl.Line. Shared by Callers, Explain and
// Impact so the path-guard/read/parse sequence is defined once.
func loadSymbol(proj *store.Project, fl FileLine) ([]byte, *analyzer.Symbol, error) {
	root := proj.Path
	if root == "" {
		root = "."
	}
	absPath := filepath.Clean(filepath.Join(root, fl.File))
	if !strings.HasPrefix(absPath, filepath.Clean(root)+string(filepath.Separator)) && absPath != filepath.Clean(root) && root != "." {
		return nil, nil, fmt.Errorf("path %q escapes project root", fl.File)
	}
	// #nosec G304 -- absPath is safely restricted within the project root
	content, err := os.ReadFile(absPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", fl.File, err)
	}

	syms := analyzer.Symbols(fl.File, content)
	if len(syms) == 0 {
		return nil, nil, fmt.Errorf("no symbols found in %s", fl.File)
	}
	return content, lookupSymbolAtLine(syms, fl.Line), nil
}

// Callers finds all files that import the package directory containing the
// symbol at the given file:line reference. It is intentionally package-level:
// the dependency graph does not claim to resolve symbol-level call sites.
func Callers(ctx context.Context, db store.IndexStore, proj *store.Project, fl FileLine) (*CallersResult, error) {
	_, targetSym, err := loadSymbol(proj, fl)
	if err != nil {
		return nil, err
	}

	graph, err := db.FetchGraphNeighbors(ctx, proj.ID)
	if err != nil {
		return nil, fmt.Errorf("fetch dependency graph: %w", err)
	}

	directCallers := findDirectCallersForDirs(graph, DependencyDirsForFile(fl.File))
	sort.Strings(directCallers)

	transitive := collectTransitiveCallers(graph, directCallers, fl.File)

	return &CallersResult{
		Symbol:     targetSym,
		Direct:     directCallers,
		Transitive: transitive,
	}, nil
}

// lookupSymbolAtLine finds the symbol at or closest to the given line.
func lookupSymbolAtLine(syms []analyzer.Symbol, line int) *analyzer.Symbol {
	// Find the symbol at or closest to the given line.
	for _, s := range syms {
		if line >= s.StartLine && line <= s.EndLine {
			return &s
		}
	}
	// No exact match: find the nearest symbol above the line.
	var nearest *analyzer.Symbol
	for _, s := range syms {
		if line >= s.StartLine {
			if nearest == nil || s.StartLine > nearest.StartLine {
				nearest = &s
			}
		}
	}
	if nearest == nil {
		nearest = &syms[0]
	}
	return nearest
}

// DependencyDirsForFile returns the graph directory keys that can identify the
// package containing file. Python projects commonly store importable modules
// below src/ or lib/, while Go and other languages retain the full directory.
// TypeScript/JavaScript imports frequently refer to a file without its
// extension (e.g. "./pages/LibraryPage" for "LibraryPage.tsx"), so we also
// return a directory keyed by the file's basename so callers can match those
// import forms.
func DependencyDirsForFile(file string) []string {
	clean := filepath.ToSlash(filepath.Clean(file))
	dir := filepath.ToSlash(filepath.Dir(clean))
	ext := strings.ToLower(filepath.Ext(clean))
	if ext == ".py" {
		return dependencyDirs(dir)
	}
	if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" || ext == ".mjs" || ext == ".cjs" {
		seen := map[string]bool{}
		var result []string
		add := func(d string) {
			d = normalizeDependencyDir(d)
			if !seen[d] {
				seen[d] = true
				result = append(result, d)
			}
		}
		add(dir)
		base := strings.TrimSuffix(filepath.Base(clean), ext)
		if base != "" && base != "." {
			add(filepath.ToSlash(filepath.Join(dir, base)))
		}
		return result
	}
	return []string{normalizeDependencyDir(dir)}
}

func dependencyDirs(dir string) []string {
	dir = filepath.ToSlash(filepath.Clean(dir))
	if dir == "." {
		dir = ""
	}
	var result []string
	seen := make(map[string]bool)
	add := func(value string) {
		value = normalizeDependencyDir(value)
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	add(dir)
	parts := strings.Split(strings.Trim(dir, "/"), "/")
	for i, part := range parts {
		if !isPythonSourceRoot(part) || i+1 >= len(parts) {
			continue
		}
		add(strings.Join(parts[i+1:], "/"))
	}
	return result
}

func isPythonSourceRoot(name string) bool {
	switch name {
	case "lib", "python", "source", "src":
		return true
	default:
		return false
	}
}

func normalizeDependencyDir(dir string) string {
	dir = filepath.ToSlash(filepath.Clean(dir))
	if dir == "." {
		return "./"
	}
	return strings.TrimSuffix(dir, "/") + "/"
}

// findDirectCallers returns all files that import the given directory. It is
// kept as a narrow helper for the existing package-level code-intel tests.
func findDirectCallers(graph map[string][]string, fileDir string) []string {
	return findDirectCallersForDirs(graph, []string{fileDir})
}

func findDirectCallersForDirs(graph map[string][]string, fileDirs []string) []string {
	wanted := make(map[string]bool, len(fileDirs))
	for _, dir := range fileDirs {
		wanted[normalizeDependencyDir(dir)] = true
	}
	var callers []string
	for src, targets := range graph {
		for _, tgt := range targets {
			if wanted[normalizeDependencyDir(tgt)] {
				callers = append(callers, src)
				break
			}
		}
	}
	return callers
}

// collectTransitiveCallers finds all transitive importers of the direct callers.
func collectTransitiveCallers(graph map[string][]string, directCallers []string, excludeFile string) []string {
	transitive := make(map[string]bool)
	for _, dc := range directCallers {
		for _, src := range findDirectCallersForDirs(graph, DependencyDirsForFile(dc)) {
			if src != excludeFile {
				transitive[src] = true
			}
		}
	}
	for _, dc := range directCallers {
		delete(transitive, dc)
	}
	tcList := make([]string, 0, len(transitive))
	for t := range transitive {
		tcList = append(tcList, t)
	}
	sort.Strings(tcList)
	return tcList
}
