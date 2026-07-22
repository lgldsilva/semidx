package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/codeintel"
	"github.com/lgldsilva/semidx/internal/deadcode"
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

			resolved, err := codeintel.ResolveProject(ctx, db, projectArg)
			if err != nil {
				return err
			}

			result, err := codeintel.DeadCode(ctx, db, resolved)
			if err != nil {
				return fmt.Errorf("dead code analysis: %w", err)
			}

			printDeadCodeResults(result.Findings, result.Stats)
			return nil
		},
	}
	c.Flags().StringVar(&projectArg, "project", "", "Project path or name (default: the project enclosing the current directory)")
	return c
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
