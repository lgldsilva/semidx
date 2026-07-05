package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/pending"
	"github.com/lgldsilva/semidx/internal/store"
)

// newUnlockCmd indexes the password-protected files that `index` recorded as
// pending. It prompts for passwords (hidden) and tries each entered password
// against ALL still-locked files — one password unlocks every file that shares
// it. Passwords are ephemeral: nothing is written to disk.
func newUnlockCmd(d *deps) *cobra.Command {
	var projectPath, model string
	var docs, verbose bool
	c := &cobra.Command{
		Use:   "unlock",
		Short: "Index password-protected files that `index` skipped (prompts for passwords)",
		Long: "Index the password-protected files that a previous `index` run skipped.\n\n" +
			"Pass the SAME --project path (and --docs, if used) you indexed with. You'll be\n" +
			"prompted for passwords with no echo; each password is tried against every\n" +
			"still-locked file, so one password unlocks all files that share it. Passwords\n" +
			"are never stored. Press Enter on an empty prompt to stop and keep the rest pending.",
		Example: "  semidx unlock --project .\n  semidx unlock --project ./docs --docs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return d.runUnlock(cmd.Context(), projectPath, model, docs, verbose)
		},
	}
	c.Flags().StringVar(&projectPath, "project", "", "Path to the project directory (same as `index`)")
	c.Flags().StringVar(&model, "model", "", "Embedding model (defaults to the one the project was indexed with)")
	c.Flags().BoolVar(&docs, "docs", false, "Treat the path as a document folder (match how it was indexed)")
	c.Flags().BoolVar(&verbose, "verbose", false, "Show per-file progress")
	_ = c.MarkFlagRequired("project")
	return c
}

// runUnlock indexes the still-pending password-protected files for a project:
// it loads the pending list, prompts for passwords, and persists whatever
// remains locked.
func (d *deps) runUnlock(ctx context.Context, projectPath, model string, docs, verbose bool) error {
	if d.remote() {
		return fmt.Errorf("unlock decrypts and indexes locally; it is not available in remote mode")
	}
	db, err := d.indexStore(ctx)
	if err != nil {
		return err
	}
	tgt := resolveTarget(ctx, projectPath, docs)

	reg, err := pending.Load(tgt.identity)
	if err != nil {
		return fmt.Errorf("load pending list: %w", err)
	}
	if reg == nil || len(reg.Files) == 0 {
		fmt.Printf("No files are pending a password for %q.\n", tgt.name)
		return nil
	}
	model = resolveUnlockModel(reg, model)

	idx, projectID, err := d.prepareUnlockIndexer(ctx, db, tgt, model, verbose)
	if err != nil {
		return err
	}

	fmt.Printf("%d file(s) need a password in %q.\n", len(reg.Files), tgt.name)
	remaining, err := unlockPending(ctx, idx, projectID, tgt.indexPath, model, reg.Files)
	if err != nil {
		return err
	}

	reg.Files = remaining
	if err := pending.Save(tgt.identity, reg); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] could not update the pending list: %v\n", err)
	}
	printUnlockSummary(remaining, tgt.sourceType)
	return nil
}

// resolveUnlockModel picks the model to embed with: an explicit --model, else
// the one the project was indexed with, else the default.
func resolveUnlockModel(reg *pending.Registry, model string) string {
	if model == "" {
		model = reg.Model // reuse the model the project was indexed with
	}
	if model == "" {
		model = "bge-m3"
	}
	return model
}

// prepareUnlockIndexer resolves dims, ensures the chunks table and project
// identity exist, and builds the indexer used to unlock files.
func (d *deps) prepareUnlockIndexer(ctx context.Context, db store.IndexStore, tgt indexTarget, model string, verbose bool) (*indexing.Indexer, int, error) {
	dims, err := d.modelDims(ctx, model)
	if err != nil {
		return nil, 0, fmt.Errorf("model info for %s: %w", model, err)
	}
	if err := db.EnsureChunksTable(ctx, dims); err != nil {
		return nil, 0, fmt.Errorf("ensure chunks table: %w", err)
	}
	projectID, err := db.EnsureProjectIdentity(ctx, tgt.identity, tgt.name, tgt.indexPath, model, tgt.sourceType, dims)
	if err != nil {
		return nil, 0, fmt.Errorf("register project: %w", err)
	}
	idx := indexing.NewIndexer(db, d.emb, dims, indexing.IndexerOpts{
		Workers:             d.cfg.IndexWorkers,
		EmbedBatchSize:      d.cfg.EmbedBatchSize,
		MaxFileSize:         d.cfg.MaxFileSize,
		MaxChunksPerFile:    d.cfg.MaxChunksPerFile,
		MaxChunksPerProject: d.cfg.MaxChunksPerProject,
		Verbose:             verbose,
	}).
		SetKeywordOnly(d.cfg.KeywordOnly).
		SetWorktree(tgt.worktree)
	return idx, projectID, nil
}

// unlockPending prompts for passwords and tries each against every still-locked
// file (one password unlocks all files that share it), until the user enters an
// empty password or nothing remains. Returns the files still locked.
func unlockPending(ctx context.Context, idx *indexing.Indexer, projectID int, indexPath, model string, remaining []string) ([]string, error) {
	for len(remaining) > 0 {
		pw, rerr := readSecret(fmt.Sprintf("Password (%d pending; empty to finish): ", len(remaining)))
		if rerr != nil {
			return remaining, rerr
		}
		if pw == "" {
			break
		}
		stillLocked, unlocked := tryPasswordOnFiles(ctx, idx, projectID, indexPath, model, pw, remaining)
		remaining = stillLocked
		fmt.Printf("  unlocked %d; %d still pending\n", unlocked, len(remaining))
	}
	return remaining, nil
}

// tryPasswordOnFiles attempts one password against each still-locked file,
// returning the files that stayed locked and how many were unlocked.
func tryPasswordOnFiles(ctx context.Context, idx *indexing.Indexer, projectID int, indexPath, model, pw string, files []string) (stillLocked []string, unlocked int) {
	for _, abs := range files {
		rel, e := filepath.Rel(indexPath, abs)
		if e != nil {
			rel = filepath.Base(abs)
		}
		ok, _, ierr := idx.IndexEncryptedFile(ctx, projectID, abs, rel, model, pw)
		switch {
		case ierr != nil:
			fmt.Fprintf(os.Stderr, "  [warn] %s: %v\n", rel, ierr)
			stillLocked = append(stillLocked, abs)
		case ok:
			unlocked++
		default:
			stillLocked = append(stillLocked, abs)
		}
	}
	return stillLocked, unlocked
}

// printUnlockSummary reports how many files remain locked and, for git projects,
// reminds the user to re-run `semidx index` for worktree-scoped search.
func printUnlockSummary(remaining []string, sourceType string) {
	if len(remaining) == 0 {
		fmt.Println("All files unlocked and indexed.")
	} else {
		fmt.Printf("%d file(s) still locked (kept pending for a later `semidx unlock`).\n", len(remaining))
	}
	if sourceType == "git" {
		fmt.Println("note: for a git project, re-run `semidx index` so unlocked files appear in worktree-scoped search.")
	}
}

// stdinReader is shared across prompts so buffered bytes (e.g. from piped input)
// aren't lost between reads. Only used on the non-TTY path.
var stdinReader = bufio.NewReader(os.Stdin)

// readSecret reads a line with no echo when stdin is a terminal; otherwise it
// reads a plain line, so piped/non-TTY input still works (never blocks a script).
func readSecret(prompt string) (string, error) {
	fmt.Print(prompt)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Println()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	line, err := stdinReader.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
