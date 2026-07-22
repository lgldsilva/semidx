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

const (
	defaultImpactDepth = 5
	maxImpactDepth     = 10
)

// ImpactNode is one file in the reverse-dependency blast radius.
type ImpactNode struct {
	File  string
	Depth int
}

// ImpactResult is the transitive reverse-dependency closure from a symbol's package.
type ImpactResult struct {
	Symbol     *analyzer.Symbol
	Affected   []ImpactNode // sorted by depth then path
	TotalCount int
}

// Impact computes the bounded reverse-dependency closure (blast radius) for the
// package containing the symbol at fl. Depth 1 = direct importers of the
// symbol's directory; each subsequent level walks importers of the previous
// level's directories, up to maxDepth (default 5, hard cap 10).
func Impact(ctx context.Context, db store.IndexStore, proj *store.Project, fl FileLine, maxDepth int) (*ImpactResult, error) {
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

	maxDepth = clampImpactDepth(maxDepth)
	fileDir := filepath.Dir(fl.File) + "/"
	affected := reverseDependencyClosure(graph, fileDir, fl.File, maxDepth)

	return &ImpactResult{
		Symbol:     targetSym,
		Affected:   affected,
		TotalCount: len(affected),
	}, nil
}

func clampImpactDepth(d int) int {
	if d <= 0 {
		return defaultImpactDepth
	}
	if d > maxImpactDepth {
		return maxImpactDepth
	}
	return d
}

// reverseDependencyClosure walks the import graph in reverse from seedDir up to
// maxDepth levels. seedFile is excluded from the result (the changed file itself
// is not "affected by" the change — its dependents are).
func reverseDependencyClosure(graph map[string][]string, seedDir, seedFile string, maxDepth int) []ImpactNode {
	visited := make(map[string]int) // file -> first-seen depth
	currentDirs := []string{seedDir}

	for depth := 1; depth <= maxDepth; depth++ {
		nextDirSet := make(map[string]bool)
		var nextDirs []string
		for _, dir := range currentDirs {
			for _, caller := range findDirectCallers(graph, dir) {
				if caller == seedFile {
					continue
				}
				if _, seen := visited[caller]; seen {
					continue
				}
				visited[caller] = depth
				cDir := filepath.Dir(caller) + "/"
				if !nextDirSet[cDir] {
					nextDirSet[cDir] = true
					nextDirs = append(nextDirs, cDir)
				}
			}
		}
		if len(nextDirs) == 0 {
			break
		}
		currentDirs = nextDirs
	}

	out := make([]ImpactNode, 0, len(visited))
	for file, depth := range visited {
		out = append(out, ImpactNode{File: file, Depth: depth})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Depth != out[j].Depth {
			return out[i].Depth < out[j].Depth
		}
		return out[i].File < out[j].File
	})
	return out
}
