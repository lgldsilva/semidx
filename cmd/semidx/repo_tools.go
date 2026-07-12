package main

import (
	"context"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/repotools"
	"github.com/lgldsilva/semidx/internal/store"
)

// printBranchList formats and prints a slice of branches to stdout.
// It sorts them (current first, then local alpha, then remote alpha) before printing.
func printBranchList(branches []repotools.Branch, repoName string) error {
	if len(branches) == 0 {
		fmt.Printf("Branches of %s: none\n", repoName)
		return nil
	}
	// Sort: current first, then local (alpha), then remote (alpha).
	sort.SliceStable(branches, func(i, j int) bool {
		if branches[i].Current != branches[j].Current {
			return branches[i].Current
		}
		if branches[i].Remote != branches[j].Remote {
			return !branches[i].Remote
		}
		return branches[i].Name < branches[j].Name
	})
	fmt.Printf("Branches of %s:\n", repoName)
	for _, b := range branches {
		kind := "local"
		if b.Remote {
			kind = "remote"
		}
		suffix := ""
		if b.Current {
			suffix = "  ← current"
		}
		fmt.Printf("  %-24s (%s)%s\n", b.Name, kind, suffix)
	}
	return nil
}

const projectPathDesc = "Path to the project directory (default: current directory)"

// resolveProjectForGit resolves the project path and validates it is a git
// repository. Returns the resolved target or an error if not a git repo.
func resolveProjectForGit(ctx context.Context, projectPath string) (indexTarget, error) {
	tgt := resolveTarget(ctx, projectPath, false)
	if tgt.sourceType != "git" {
		return tgt, fmt.Errorf("not a git repository")
	}
	return tgt, nil
}

func newRepoWorktreesCmd(d *deps) *cobra.Command {
	var projectPath string
	c := &cobra.Command{
		Use:   "worktrees",
		Short: "List worktrees of the repository",
		Long: `List all git worktrees (checkouts) associated with the repository at the
given path. Uses 'git worktree list --porcelain' for machine-parseable output.`,
		Example: `  semidx repo worktrees                      # current directory
  semidx repo worktrees --project ./my-repo`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			tgt, err := resolveProjectForGit(ctx, projectPath)
			if err != nil {
				return err
			}
			wts, err := repotools.ListWorktrees(ctx, tgt.indexPath)
			if err != nil {
				return fmt.Errorf("list worktrees: %w", err)
			}
			if len(wts) == 0 {
				fmt.Printf("Worktrees of %s: none\n", tgt.name)
				return nil
			}
			fmt.Printf("Worktrees of %s:\n", tgt.name)
			for _, wt := range wts {
				branch := wt.Branch
				if branch == "" {
					branch = "detached"
				}
				if wt.HEAD != "" {
					fmt.Printf("  %s  (%s @ %s)\n", wt.Path, branch, wt.HEAD)
				} else {
					fmt.Printf("  %s  (%s)\n", wt.Path, branch)
				}
			}
			return nil
		},
	}
	c.Flags().StringVar(&projectPath, "project", ".", projectPathDesc)
	return c
}

func newRepoBranchesCmd(d *deps) *cobra.Command {
	var projectPath string
	var includeRemote bool
	c := &cobra.Command{
		Use:   "branches",
		Short: "List branches of the repository",
		Long: `List all local (and optionally remote) branches of the repository at the
given path. The current branch is marked with an arrow. Remote branches are
shown prefixed by their remote name (e.g. origin/main).`,
		Example: `  semidx repo branches                        # local + remote branches
  semidx repo branches --remote=false          # local branches only
  semidx repo branches --project ./my-repo`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			tgt, err := resolveProjectForGit(ctx, projectPath)
			if err != nil {
				return err
			}
			branches, err := repotools.ListBranches(ctx, tgt.indexPath, includeRemote)
			if err != nil {
				return fmt.Errorf("list branches: %w", err)
			}
			return printBranchList(branches, tgt.name)
		},
	}
	c.Flags().StringVar(&projectPath, "project", ".", projectPathDesc)
	c.Flags().BoolVar(&includeRemote, "remote", true, "Include remote branches")
	return c
}

func newRepoInfoCmd(d *deps) *cobra.Command {
	var projectPath string
	var includeRemote bool
	c := &cobra.Command{
		Use:   "info",
		Short: "Show repository and index information",
		Long: `Show detailed information about a repository: git state (worktrees, branches,
working tree cleanliness) and the semidx index state (model, file count, status).
For non-git projects (document folders), only the index state is shown.`,
		Example: `  semidx repo info                            # current directory
  semidx repo info --project ./my-repo
  semidx repo info --remote=false`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			tgt := resolveTarget(ctx, projectPath, false)

			if tgt.sourceType == "git" {
				return printRepoInfo(ctx, d, tgt, includeRemote)
			}
			return printDocInfo(ctx, d, tgt)
		},
	}
	c.Flags().StringVar(&projectPath, "project", ".", projectPathDesc)
	c.Flags().BoolVar(&includeRemote, "remote", true, "Include remote branches in branch count")
	return c
}

func printRepoInfo(ctx context.Context, d *deps, tgt indexTarget, includeRemote bool) error {
	// Git state.
	status, err := repotools.Status(ctx, tgt.indexPath)
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}

	wts, err := repotools.ListWorktrees(ctx, tgt.indexPath)
	if err != nil {
		return fmt.Errorf("list worktrees: %w", err)
	}

	branches, err := repotools.ListBranches(ctx, tgt.indexPath, includeRemote)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}

	fmt.Printf("Repository: %s\n", tgt.name)
	fmt.Printf("Path: %s\n", tgt.indexPath)
	fmt.Printf("Identity: %s\n", tgt.identity)

	fmt.Println("\nGit:")
	fmt.Printf("  Worktrees: %d\n", len(wts))
	for _, wt := range wts {
		branch := wt.Branch
		if branch == "" {
			branch = "detached"
		}
		fmt.Printf("    - %s  (%s @ %s)\n", wt.Path, branch, wt.HEAD)
	}

	branchSuffix := ""
	if !includeRemote {
		branchSuffix = " (use --remote to see remote branches)"
	}
	fmt.Printf("  Branches: %d%s\n", len(branches), branchSuffix)

	clean := "clean"
	if status.Dirty {
		clean = "dirty"
	}
	current := status.CurrentBranch
	if current == "" {
		current = "detached"
	}
	fmt.Printf("  Working tree: %s (current: %s)\n", clean, current)
	fmt.Printf("  HEAD: %s\n", status.HEAD)

	// Index state.
	return printIndexState(ctx, d, tgt)
}

func printDocInfo(ctx context.Context, d *deps, tgt indexTarget) error {
	fmt.Printf("Repository: %s\n", tgt.name)
	fmt.Printf("Path: %s\n", tgt.indexPath)
	fmt.Printf("Identity: %s\n", tgt.identity)
	fmt.Println("\nGit:\n  (not a git repository)")
	return printIndexState(ctx, d, tgt)
}

func printIndexState(ctx context.Context, d *deps, tgt indexTarget) error {
	fmt.Println("\nIndex (semidx):")
	fmt.Printf("  Project: %s\n", tgt.name)

	db, err := d.indexStore(ctx)
	if err != nil {
		fmt.Printf("  Status: store unavailable (%v)\n", err)
		return nil
	}

	proj, err := db.GetProjectByIdentity(ctx, tgt.identity)
	if err != nil {
		if err == store.ErrNotFound {
			fmt.Printf("  Status: not indexed\n")
			return nil
		}
		// Try by name as fallback.
		proj, err = db.GetProject(ctx, tgt.name)
		if err != nil {
			if err == store.ErrNotFound {
				fmt.Printf("  Status: not indexed\n")
				return nil
			}
			fmt.Printf("  Status: lookup error (%v)\n", err)
			return nil
		}
	}

	fmt.Printf("  Status: %s\n", proj.Status)
	if proj.Model != "" {
		fmt.Printf("  Model: %s\n", proj.Model)
	}
	count, err := db.CountProjectFiles(ctx, proj.ID)
	if err != nil {
		fmt.Printf("  Indexed files: error (%v)\n", err)
	} else {
		fmt.Printf("  Indexed files: %d\n", count)
	}
	return nil
}
