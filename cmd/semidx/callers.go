package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/codeintel"
)

// newCallersCmd returns the `semidx callers` command, which shows which files
// import a given file (and thereby may call the symbols it defines).
func newCallersCmd(d *deps) *cobra.Command {
	var projectArg string
	c := &cobra.Command{
		Use:   "callers <file:line>",
		Short: "Show files that import a given source file",
		Long: `Show all indexed files that import the file containing a symbol at the given
file:line reference. Uses the indexed dependency graph (file_dependencies) to
resolve reverse imports.

The file path is relative to the project root, matching how the index stores it.

Examples:

  semidx callers internal/auth/token.go:42
  semidx callers pkg/client/client.go:1 --project ./my-repo`,
		Args: cobra.ExactArgs(1),
		Example: `  semidx callers internal/auth/token.go:42
  semidx callers internal/store/store.go:1 --project ./my-repo`,
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

			result, err := codeintel.Callers(ctx, db, resolved, fl)
			if err != nil {
				return err
			}

			printCallersResult(result)
			return nil
		},
	}
	c.Flags().StringVar(&projectArg, "project", "", "Project path or name (default: the project enclosing the current directory)")
	return c
}

func printCallersResult(result *codeintel.CallersResult) {
	fmt.Printf("\n  Callers of: %s\n", result.Symbol.Name)
	fmt.Println("  " + strings.Repeat("─", 60))

	fmt.Printf("  Direct (%d):\n", len(result.Direct))
	if len(result.Direct) == 0 {
		fmt.Println("    (none — no indexed file imports this package)")
	} else {
		for _, c := range result.Direct {
			fmt.Printf("    %s\n", c)
		}
	}

	if len(result.Transitive) > 0 {
		fmt.Printf("\n  Transitive (%d):\n", len(result.Transitive))
		for _, t := range result.Transitive {
			fmt.Printf("    %s\n", t)
		}
	}
}
