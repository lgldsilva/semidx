package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/store"
)

// projectType holds info about an auto-detected project type.
type projectType struct {
	Type string // e.g. "Go", "Node.js", "Rust", "Python", "Java", "Makefile"
	File string // the detection file (relative to root)
}

// detectProjects scans root for known project markers and returns the detected
// types (may be empty).
func detectProjects(root string) []projectType {
	var pts []projectType
	markers := map[string]string{
		"go.mod":         "Go",
		"package.json":   "Node.js",
		"Cargo.toml":     "Rust",
		"pyproject.toml": "Python",
		"pom.xml":        "Java",
		"Makefile":       "Makefile",
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	// Build a set for O(1) lookups.
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name()] = true
	}
	for marker, typ := range markers {
		if names[marker] {
			pts = append(pts, projectType{Type: typ, File: marker})
		}
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].Type < pts[j].Type })
	return pts
}

// findNearbyGitRepos walks parent directories up to 3 levels deep looking for
// git repos (directories containing a .git subdirectory).
func findNearbyGitRepos(root string) []string {
	var repos []string
	seen := make(map[string]bool)

	// Walk the parent directory tree up to 3 levels up.
	for depth := 0; depth < 3; depth++ {
		entries, err := os.ReadDir(root)
		if err != nil {
			return repos
		}
		// Check entries for .git subdirectories at this level.
		for _, e := range entries {
			if !e.IsDir() || seen[e.Name()] {
				continue
			}
			gitDir := filepath.Join(root, e.Name(), ".git")
			if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
				repos = append(repos, filepath.Join(root, e.Name()))
				seen[e.Name()] = true
			}
		}

		// Also check if root itself is a git repo.
		if !seen[filepath.Base(root)] {
			gi := gitmeta.Resolve(context.Background(), root)
			if gi.IsGit {
				repos = append(repos, gi.Toplevel)
				seen[filepath.Base(root)] = true
			}
		}

		// Move up.
		parent := filepath.Dir(root)
		if parent == root {
			break
		}
		root = parent
	}
	sort.Strings(repos)
	return repos
}

// promptYesNo asks a yes/no question on stdin and returns true for Y/yes.
func promptYesNo(scanner *bufio.Scanner, question string, defaultYes bool) bool {
	suffix := "[Y/n]"
	if !defaultYes {
		suffix = "[y/N]"
	}
	fmt.Printf("%s %s: ", question, suffix)
	if !scanner.Scan() {
		return defaultYes
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	switch answer {
	case "y", "yes", "yeah", "ye":
		return true
	case "n", "no", "nope":
		return false
	default:
		return defaultYes
	}
}

// promptString asks for a string value with a default.
func promptString(scanner *bufio.Scanner, question, defaultVal string) string {
	def := defaultVal
	if def == "" {
		def = "(none)"
	}
	fmt.Printf("%s [%s]: ", question, def)
	if !scanner.Scan() {
		return defaultVal
	}
	answer := strings.TrimSpace(scanner.Text())
	if answer == "" {
		return defaultVal
	}
	return answer
}

// promptSelect asks the user to choose one of several options.
type initOpts struct {
	yes     bool
	keyword bool
}

func newInitCmd(d *deps) *cobra.Command {
	var opts initOpts

	c := &cobra.Command{
		Use:   "init",
		Short: "Initialize semidx for a project (interactive setup wizard)",
		Long: `Interactive first-time project setup wizard. Auto-detects your project
type (Go, Node.js, Rust, etc.), finds nearby git repos, and guides you through
indexing — all with sensible defaults.

Pass --yes to skip all prompts and index the current directory immediately.`,
		Example: `  semidx init                            # interactive setup
  semidx init --yes                      # non-interactive, index current dir
  semidx init --yes --keyword            # non-interactive, keyword-only`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInit(cmd, d, opts)
		},
	}

	c.Flags().BoolVar(&opts.yes, "yes", false, "Non-interactive mode: accept all defaults and index immediately")
	c.Flags().BoolVar(&opts.keyword, "keyword", false, "Keyword-only (no embeddings)")
	return c
}

func runInit(cmd *cobra.Command, d *deps, opts initOpts) error {
	ctx := cmd.Context()
	scanner := bufio.NewScanner(os.Stdin)

	// Detect the root directory.
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	if !opts.yes {
		fmt.Println("╭──────────────────────────────────────────────╮")
		fmt.Println("│  semidx — interactive project setup          │")
		fmt.Println("╰──────────────────────────────────────────────╯")
		fmt.Println()
	}

	// Step 1: Detect project type.
	types := detectProjects(root)
	projectPath := root

	if len(types) > 0 {
		typeNames := make([]string, len(types))
		for i, pt := range types {
			typeNames[i] = pt.Type
		}
		fmt.Printf("Detected project type: %s\n", strings.Join(typeNames, ", "))
	} else {
		fmt.Println("No known project files found (go.mod, package.json, etc.)")
	}

	if !opts.yes {
		projectPath = promptString(scanner, "Project root", root)
		abs, err := filepath.Abs(projectPath)
		if err == nil {
			projectPath = abs
		}
	}

	// Step 2: Ask to index.
	shouldIndex := true
	if !opts.yes {
		shouldIndex = promptYesNo(scanner, "Index this project?", true)
	}

	if !shouldIndex {
		fmt.Println("\nProject registered. Index later with:")
		fmt.Printf("  semidx index --project %s\n", projectPath)
		return nil
	}

	// Step 3: Detect nearby git repos and offer to index them too.
	nearby := findNearbyGitRepos(projectPath)
	var extraPaths []string
	if len(nearby) > 0 && !opts.yes {
		fmt.Println("\nNearby git repositories found:")
		for _, r := range nearby {
			fmt.Printf("  - %s\n", r)
		}
		if promptYesNo(scanner, "Index these too?", false) {
			extraPaths = nearby
		}
	}

	// Step 4: Run index on the primary project.
	fmt.Println()
	keyword := opts.keyword || d.keywordOnly
	model := "bge-m3"
	if keyword {
		model = "keyword"
	}

	if err := indexProject(ctx, d, projectPath, model, keyword); err != nil {
		return fmt.Errorf("index %s: %w", projectPath, err)
	}

	// Step 5: Index nearby repos.
	for _, p := range extraPaths {
		fmt.Println()
		if err := indexProject(ctx, d, p, model, keyword); err != nil {
			fmt.Fprintf(os.Stderr, "[warn] index %s: %v\n", p, err)
		}
	}

	// Step 6: Summary.
	fmt.Println()
	fmt.Println("╭──────────────────────────────────────────────╮")
	fmt.Println("│  semidx is ready!                            │")
	fmt.Println("╰──────────────────────────────────────────────╯")
	fmt.Println()
	fmt.Println("Try searching your code:")
	fmt.Printf("  semidx search --project %s --query \"how is auth handled\"\n", projectPath)
	fmt.Println()
	fmt.Println("Or use sgrep for grep-style output:")
	fmt.Printf("  semidx sgrep --project %s --query \"database pool\"\n", projectPath)
	fmt.Println()
	if !keyword {
		fmt.Println("Configure an embedding provider for better results:")
		fmt.Println("  semidx config set GEMINI_API_KEY <key>")
	}
	fmt.Println("See semidx --help for all commands.")

	return nil
}

// indexProject runs the full indexing pipeline for a single path.
func indexProject(ctx context.Context, d *deps, projectPath, model string, keyword bool) error {
	fmt.Printf("Indexing %s...\n", projectPath)

	db, err := d.indexStore(ctx)
	if err != nil {
		return err
	}

	// Resolve the target (git or docs).
	tgt := resolveTarget(ctx, projectPath, false)

	dims := store.KeywordDims
	if !keyword {
		mdims, err := d.modelDims(ctx, model)
		if err != nil {
			return fmt.Errorf("model info for %s: %w (no provider reachable? re-run with --keyword to index text-only)", model, err)
		}
		dims = mdims
	}

	if err := db.EnsureChunksTable(ctx, dims); err != nil {
		return fmt.Errorf("ensure chunks table: %w", err)
	}

	projectID, err := db.EnsureProjectIdentity(ctx, tgt.identity, tgt.name, tgt.indexPath, model, tgt.sourceType, dims)
	if err != nil {
		return fmt.Errorf("register project: %w", err)
	}

	indexer := indexing.NewIndexer(db, d.emb, dims, indexing.IndexerOpts{
		Workers:          d.cfg.IndexWorkers,
		EmbedBatchSize:   d.cfg.EmbedBatchSize,
		MaxFileSize:      d.cfg.MaxFileSize,
		MaxChunksPerFile: d.cfg.MaxChunksPerFile,
		Verbose:          false,
	}).
		SetKeywordOnly(keyword).
		SetWorktree(tgt.worktree)

	start := time.Now()
	stats, err := indexer.IndexProject(ctx, projectID, tgt.indexPath, model, 0)
	if err != nil {
		return fmt.Errorf("index: %w", err)
	}

	fmt.Printf("  ✓ %d files scanned, %d indexed, %d chunks in %v\n",
		stats.FilesScanned, stats.FilesIndexed, stats.ChunksCreated, time.Since(start))
	return nil
}
