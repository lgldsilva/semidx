package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/deadcode"
	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/projectref"
	"github.com/lgldsilva/semidx/internal/store"
)

// newDeadCodeCmd returns the `semidx dead-code` command, which analyses an
// indexed project's files and reports symbols with no incoming dependencies.
func newDeadCodeCmd(d *deps) *cobra.Command {
	var projectArg string
	c := &cobra.Command{
		Use:   "dead-code",
		Short: "Find unused symbols in an indexed project",
		Long: `Analyse all indexed files of a project and report symbols that appear to be
dead — no other file imports their package. Uses the indexed dependency graph
(file_dependencies) and reads source files from disk to extract symbols via
tree-sitter.

Classification:
  confirmed   — unexported symbol whose package has no importers (safe to delete)
  public-api  — exported symbol whose package has no importers (review needed)

Examples:

  semidx dead-code                          # analyse the project enclosing cwd
  semidx dead-code --project ./my-repo      # analyse a specific project`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			db, err := d.indexStore(ctx)
			if err != nil {
				return err
			}

			resolved, err := resolveDeadCodeTarget(ctx, db, projectArg)
			if err != nil {
				return err
			}

			root := resolved.Path
			if root == "" {
				root = "."
			}

			findings, err := deadcode.Analyze(ctx, resolved.ID, db, root)
			if err != nil {
				return fmt.Errorf("dead code analysis: %w", err)
			}

			stats := deadcode.AggregateStats(findings)
			printDeadCodeResults(findings, stats)
			return nil
		},
	}
	c.Flags().StringVar(&projectArg, "project", "", "Project path or name (default: the project enclosing the current directory)")
	return c
}

// resolveDeadCodeTarget resolves the project for dead-code analysis.
func resolveDeadCodeTarget(ctx context.Context, db store.IndexStore, projectArg string) (*store.Project, error) {
	if projectArg != "" {
		return projectref.Resolve(ctx, db, projectArg)
	}
	// No argument: try the enclosing git repo.
	if gi := gitmeta.Resolve(ctx, "."); gi.IsGit {
		if p, err := db.GetProjectByIdentity(ctx, gi.Identity); err == nil {
			return p, nil
		}
	}
	// Fall back to listing and picking an enclosing project.
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

// printDeadCodeResults formats dead-code findings grouped by confidence.
func printDeadCodeResults(findings []deadcode.Finding, stats deadcode.Stats) {
	if len(findings) == 0 {
		fmt.Println("No dead code found.")
		return
	}

	// Group by confidence.
	var confirmed, publicAPI []deadcode.Finding
	for _, f := range findings {
		switch f.Confidence {
		case "confirmed":
			confirmed = append(confirmed, f)
		case "public-api":
			publicAPI = append(publicAPI, f)
		}
	}

	if len(confirmed) > 0 {
		fmt.Println("Confirmed dead (safe to delete):")
		for _, f := range confirmed {
			fmt.Printf("  %s:%d  %s (%s)\n", f.File, f.StartLine, f.Symbol, f.Kind)
		}
		fmt.Println()
	}

	if len(publicAPI) > 0 {
		fmt.Println("Likely dead (review needed):")
		for _, f := range publicAPI {
			fmt.Printf("  %s:%d  %s (%s, exported)\n", f.File, f.StartLine, f.Symbol, f.Kind)
		}
		fmt.Println()
	}

	// Count unique files.
	uniqueFiles := make(map[string]bool)
	for _, f := range findings {
		uniqueFiles[f.File] = true
	}

	fmt.Printf("Total dead: %d symbols (%d files)\n", len(findings), len(uniqueFiles))
}
