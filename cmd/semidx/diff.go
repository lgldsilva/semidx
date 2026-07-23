package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/codeintel"
)

func newDiffCmd(d *deps) *cobra.Command {
	return &cobra.Command{
		Use:   "diff <ref1>..<ref2>",
		Short: "Show semantic diff between two git refs (new/removed/changed symbols)",
		Long: `Compare two git references and report new, removed, and changed code symbols.
Accepts both "ref1..ref2" (changes in ref2 since ref1) and "ref1...ref2"
(changes introduced by ref2 since it diverged from ref1) syntax.

Uses git to find changed files and extracts Go symbols from them.`,
		Example: `  semidx diff main..feat/oauth2
  semidx diff main...feat/oauth2`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			refSpec := args[0]
			ref1, ref2, threeDot, err := codeintel.ParseRefRange(refSpec)
			if err != nil {
				return err
			}

			result, err := codeintel.Diff("", ref1, ref2, threeDot)
			if err != nil {
				return err
			}

			printDiffResult(result)
			return nil
		},
	}
}

func printDiffResult(result *codeintel.DiffResult) {
	fmt.Printf("Semantic Diff: %s → %s\n", result.Ref1, result.Ref2)
	fmt.Println(strings.Repeat("─", 40))

	if len(result.New) > 0 {
		fmt.Printf("\nNew symbols (%d):\n", len(result.New))
		for _, dr := range result.New {
			fmt.Printf("  + %s (%s:%d)\n", dr.Name, dr.FilePath, dr.Line)
		}
	}
	if len(result.Removed) > 0 {
		fmt.Printf("\nRemoved symbols (%d):\n", len(result.Removed))
		for _, dr := range result.Removed {
			fmt.Printf("  - %s (%s:%d)\n", dr.Name, dr.FilePath, dr.Line)
		}
	}
	if len(result.Changed) > 0 {
		fmt.Printf("\nChanged signatures (%d):\n", len(result.Changed))
		for _, dr := range result.Changed {
			fmt.Printf("  ~ %s — changed signature\n", dr.Name)
		}
	}
	if len(result.New)+len(result.Removed)+len(result.Changed) == 0 {
		fmt.Println("No semantic differences found.")
	}
}
