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

// Callers finds all files that import the file containing the symbol at the
// given file:line reference.
func Callers(ctx context.Context, db store.IndexStore, proj *store.Project, fl FileLine) (*CallersResult, error) {
	root := proj.Path
	if root == "" {
		root = "."
	}
	absPath := filepath.Clean(filepath.Join(root, fl.File))
	if !strings.HasPrefix(absPath, filepath.Clean(root)+string(filepath.Separator)) && absPath != filepath.Clean(root) && root != "." {
		return nil, fmt.Errorf("path %q escapes project root", fl.File)
	}
	// #nosec G304 -- absPath is safely restricted within the project root
	content, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", fl.File, err)
	}

	syms := analyzer.Symbols(fl.File, content)
	if len(syms) == 0 {
		return nil, fmt.Errorf("no symbols found in %s", fl.File)
	}

	targetSym := lookupSymbolAtLine(syms, fl.Line)

	graph, err := db.FetchGraphNeighbors(ctx, proj.ID)
	if err != nil {
		return nil, fmt.Errorf("fetch dependency graph: %w", err)
	}

	fileDir := filepath.Dir(fl.File) + "/"
	directCallers := findDirectCallers(graph, fileDir)
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

// findDirectCallers returns all files that import the given directory.
func findDirectCallers(graph map[string][]string, fileDir string) []string {
	var callers []string
	for src, targets := range graph {
		for _, tgt := range targets {
			if tgt == fileDir {
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
		want := filepath.Dir(dc) + "/"
		for src, targets := range graph {
			if src == excludeFile {
				continue
			}
			for _, tgt := range targets {
				if tgt == want {
					transitive[src] = true
					break
				}
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
