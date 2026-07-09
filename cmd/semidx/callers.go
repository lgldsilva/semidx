package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/analyzer"
	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/projectref"
	"github.com/lgldsilva/semidx/internal/store"
)

// fileLineArg parses a "file:line" argument.
type fileLineArg struct {
	File string
	Line int
}

func parseFileLine(arg string) (fileLineArg, error) {
	idx := strings.LastIndex(arg, ":")
	if idx < 0 {
		return fileLineArg{}, fmt.Errorf("expected file:line format, got %q", arg)
	}
	line, err := strconv.Atoi(arg[idx+1:])
	if err != nil || line < 1 {
		return fileLineArg{}, fmt.Errorf("invalid line number in %q", arg)
	}
	return fileLineArg{File: arg[:idx], Line: line}, nil
}

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
			fl, err := parseFileLine(args[0])
			if err != nil {
				return err
			}

			db, err := d.indexStore(ctx)
			if err != nil {
				return err
			}

			resolved, err := resolveCallersProject(ctx, db, projectArg)
			if err != nil {
				return err
			}

			return printCallers(ctx, db, resolved, fl)
		},
	}
	c.Flags().StringVar(&projectArg, "project", "", "Project path or name (default: the project enclosing the current directory)")
	return c
}

// resolveCallersProject resolves the project for caller analysis.
func resolveCallersProject(ctx context.Context, db store.IndexStore, projectArg string) (*store.Project, error) {
	if projectArg != "" {
		return projectref.Resolve(ctx, db, projectArg)
	}
	if gi := gitmeta.Resolve(ctx, "."); gi.IsGit {
		if p, err := db.GetProjectByIdentity(ctx, gi.Identity); err == nil {
			return p, nil
		}
	}
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

// printCallers finds and displays the direct (and transitive) callers of the
// symbol defined at the given file:line.
func printCallers(ctx context.Context, db store.IndexStore, proj *store.Project, fl fileLineArg) error {
	root := proj.Path
	if root == "" {
		root = "."
	}
	absPath := filepath.Clean(filepath.Join(root, fl.File))
	if !strings.HasPrefix(absPath, filepath.Clean(root)+string(filepath.Separator)) && absPath != filepath.Clean(root) && root != "." {
		return fmt.Errorf("path %q escapes project root", fl.File)
	}
	// #nosec G304 -- absPath is safely restricted within the project root
	content, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", fl.File, err)
	}

	syms := analyzer.Symbols(fl.File, content)
	if len(syms) == 0 {
		return fmt.Errorf("no symbols found in %s", fl.File)
	}

	targetSym := lookupSymbolAtLine(syms, fl.Line)
	fmt.Printf("\n  Callers of: %s\n", targetSym.Name)
	fmt.Println("  " + strings.Repeat("─", 60))

	graph, err := db.FetchGraphNeighbors(ctx, proj.ID)
	if err != nil {
		return fmt.Errorf("fetch dependency graph: %w", err)
	}

	fileDir := filepath.Dir(fl.File) + "/"
	directCallers := findDirectCallers(graph, fileDir)
	sort.Strings(directCallers)

	printDirectCallers(directCallers)
	printTransitiveCallers(graph, directCallers, fl.File)

	return nil
}

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

func printDirectCallers(directCallers []string) {
	fmt.Printf("  Direct (%d):\n", len(directCallers))
	if len(directCallers) == 0 {
		fmt.Println("    (none — no indexed file imports this package)")
	} else {
		for _, c := range directCallers {
			fmt.Printf("    %s\n", c)
		}
	}
}

func printTransitiveCallers(graph map[string][]string, directCallers []string, excludeFile string) {
	if len(directCallers) == 0 {
		return
	}
	transitive := make(map[string]bool)
	for _, dc := range directCallers {
		for src, targets := range graph {
			for _, tgt := range targets {
				if tgt == filepath.Dir(dc)+"/" && src != excludeFile {
					transitive[src] = true
				}
			}
		}
	}
	// Remove direct callers from transitive set.
	for _, dc := range directCallers {
		delete(transitive, dc)
	}

	if len(transitive) == 0 {
		return
	}
	var tcList []string
	for t := range transitive {
		tcList = append(tcList, t)
	}
	sort.Strings(tcList)
	fmt.Printf("\n  Transitive (%d):\n", len(tcList))
	for _, t := range tcList {
		fmt.Printf("    %s\n", t)
	}
}
