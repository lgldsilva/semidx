package codeintel

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

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
	_, targetSym, err := loadSymbol(proj, fl)
	if err != nil {
		return nil, err
	}

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
// BFS expansion and sort are extracted so cognitive complexity stays under the
// SonarQube gate.
func reverseDependencyClosure(graph map[string][]string, seedDir, seedFile string, maxDepth int) []ImpactNode {
	visited := make(map[string]int) // file -> first-seen depth
	currentDirs := []string{seedDir}

	for depth := 1; depth <= maxDepth; depth++ {
		nextDirs := expandImpactLevel(graph, currentDirs, seedFile, depth, visited)
		if len(nextDirs) == 0 {
			break
		}
		currentDirs = nextDirs
	}
	return collectImpactNodes(visited)
}

// expandImpactLevel records direct callers of currentDirs at the given depth
// and returns the unique directories of newly discovered callers for the next level.
func expandImpactLevel(graph map[string][]string, currentDirs []string, seedFile string, depth int, visited map[string]int) []string {
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
			if nextDirSet[cDir] {
				continue
			}
			nextDirSet[cDir] = true
			nextDirs = append(nextDirs, cDir)
		}
	}
	return nextDirs
}

// collectImpactNodes materializes visited files as ImpactNodes sorted by depth
// then path.
func collectImpactNodes(visited map[string]int) []ImpactNode {
	out := make([]ImpactNode, 0, len(visited))
	for file, depth := range visited {
		out = append(out, ImpactNode{File: file, Depth: depth})
	}
	sort.Slice(out, lessImpactNode(out))
	return out
}

func lessImpactNode(out []ImpactNode) func(i, j int) bool {
	return func(i, j int) bool {
		if out[i].Depth != out[j].Depth {
			return out[i].Depth < out[j].Depth
		}
		return out[i].File < out[j].File
	}
}
