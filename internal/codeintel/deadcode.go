package codeintel

import (
	"context"

	"github.com/lgldsilva/semidx/internal/deadcode"
	"github.com/lgldsilva/semidx/internal/store"
)

// DeadCodeResult contains the results of dead code analysis.
type DeadCodeResult struct {
	Findings []deadcode.Finding
	Stats    deadcode.Stats
}

// DeadCode analyzes a project for unused symbols.
func DeadCode(ctx context.Context, db store.IndexStore, proj *store.Project) (*DeadCodeResult, error) {
	root := proj.Path
	if root == "" {
		root = "."
	}

	findings, err := deadcode.Analyze(ctx, proj.ID, db, root)
	if err != nil {
		return nil, err
	}

	stats := deadcode.AggregateStats(findings)

	return &DeadCodeResult{
		Findings: findings,
		Stats:    stats,
	}, nil
}
