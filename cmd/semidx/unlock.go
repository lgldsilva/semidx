package main

import (
	"bufio"
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
			ctx := cmd.Context()
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
			if model == "" {
				model = reg.Model // reuse the model the project was indexed with
			}
			if model == "" {
				model = "bge-m3"
			}

			dims := store.KeywordDims
			if !d.cfg.KeywordOnly {
				info, ierr := d.emb.ModelInfo(ctx, model)
				if ierr != nil {
					return fmt.Errorf("model info for %s: %w", model, ierr)
				}
				dims = info.Dims
			}
			if err := db.EnsureChunksTable(ctx, dims); err != nil {
				return fmt.Errorf("ensure chunks table: %w", err)
			}
			projectID, err := db.EnsureProjectIdentity(ctx, tgt.identity, tgt.name, tgt.indexPath, model, tgt.sourceType)
			if err != nil {
				return fmt.Errorf("register project: %w", err)
			}
			idx := indexing.NewIndexer(db, d.emb, dims, d.cfg.IndexWorkers, verbose, false, "").
				SetKeywordOnly(d.cfg.KeywordOnly).
				SetWorktree(tgt.worktree)

			remaining := reg.Files
			fmt.Printf("%d file(s) need a password in %q.\n", len(remaining), tgt.name)
			for len(remaining) > 0 {
				pw, rerr := readSecret(fmt.Sprintf("Password (%d pending; empty to finish): ", len(remaining)))
				if rerr != nil {
					return rerr
				}
				if pw == "" {
					break
				}
				var stillLocked []string
				unlocked := 0
				for _, abs := range remaining {
					rel, e := filepath.Rel(tgt.indexPath, abs)
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
				remaining = stillLocked
				fmt.Printf("  unlocked %d; %d still pending\n", unlocked, len(remaining))
			}

			reg.Files = remaining
			if err := pending.Save(tgt.identity, reg); err != nil {
				fmt.Fprintf(os.Stderr, "[warn] could not update the pending list: %v\n", err)
			}
			if len(remaining) == 0 {
				fmt.Println("All files unlocked and indexed.")
			} else {
				fmt.Printf("%d file(s) still locked (kept pending for a later `semidx unlock`).\n", len(remaining))
			}
			if tgt.sourceType == "git" {
				fmt.Println("note: for a git project, re-run `semidx index` so unlocked files appear in worktree-scoped search.")
			}
			return nil
		},
	}
	c.Flags().StringVar(&projectPath, "project", "", "Path to the project directory (same as `index`)")
	c.Flags().StringVar(&model, "model", "", "Embedding model (defaults to the one the project was indexed with)")
	c.Flags().BoolVar(&docs, "docs", false, "Treat the path as a document folder (match how it was indexed)")
	c.Flags().BoolVar(&verbose, "verbose", false, "Show per-file progress")
	_ = c.MarkFlagRequired("project")
	return c
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
