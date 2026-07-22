package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/codeintel"
)

const explainIndent = "    %s\n"

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
			fl, err := codeintel.ParseFileLine(args[0])
			if err != nil {
				return err
			}

			db, err := d.indexStore(ctx)
			if err != nil {
				return err
			}

			resolved, err := codeintel.ResolveProject(ctx, db, projectArg)
			if err != nil {
				return err
			}

			result, err := codeintel.Explain(ctx, db, resolved, fl)
			if err != nil {
				return err
			}

			printExplainResult(result)
			return nil
		},
	}
	c.Flags().StringVar(&projectArg, "project", "", "Project path or name (default: the project enclosing the current directory)")
	return c
}

func printExplainResult(result *codeintel.ExplainResult) {
	fmt.Println()
	fmt.Printf("  %s — %s (%s:%d-%d)\n", result.Display, result.Symbol.Kind, result.File, result.Symbol.StartLine, result.Symbol.EndLine)
	fmt.Println("  " + strings.Repeat("─", 80))

	fmt.Printf("\n  Dependencies (%d):\n", len(result.Imports))
	if len(result.Imports) > 0 {
		for _, dep := range result.Imports {
			fmt.Printf(explainIndent, dep)
		}
	} else {
		fmt.Println("    (none detected)")
	}

	fmt.Printf("\n  Imported by (%d files):\n", len(result.Importers))
	if len(result.Importers) > 0 {
		for _, imp := range result.Importers {
			fmt.Printf(explainIndent, imp)
		}
	} else {
		fmt.Println("    (none — this package is not imported by any indexed file)")
	}

	fmt.Printf("\n  Tests (%d files):\n", len(result.Tests))
	if len(result.Tests) > 0 {
		for _, tf := range result.Tests {
			fmt.Printf(explainIndent, tf)
		}
	} else {
		fmt.Println("    (none found)")
	}
}
