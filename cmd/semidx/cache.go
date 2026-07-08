package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// commonDims covers the embedding dimensions in regular use.
// Listing them explicitly avoids schema-discovery queries on both PG and SQLite.
var commonDims = []int{768, 1024, 1536, 3072}

func newCacheCmd(d *deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage the embedding cache",
		Long: `Manage the embedding cache used to de-duplicate API calls across
projects. Cache entries are auxiliary — pruning them only means
future index runs re-compute the embeddings; it does not affect
existing search results.`,
	}
	prune := &cobra.Command{
		Use:   "prune",
		Short: "Remove all cached embeddings",
		Long: `Remove all entries from the embedding cache. This is a truncation —
cached embeddings are deleted but chunks remain searchable. The cache
re-warms on the next index run.

Phase 1: simple full truncation per dimension (not reference-based
garbage collection).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCachePrune(cmd, d)
		},
	}
	cmd.AddCommand(prune)
	return cmd
}

// runCachePrune iterates over known embedding dimensions and calls
// PruneEmbeddingCache for each, skipping non-existent tables gracefully.
func runCachePrune(cmd *cobra.Command, d *deps) error {
	ctx := cmd.Context()
	store, err := d.indexStore(ctx)
	if err != nil {
		return fmt.Errorf("open index store: %w", err)
	}

	var total int64
	for _, dims := range commonDims {
		n, err := store.PruneEmbeddingCache(ctx, dims)
		if err != nil {
			if isMissingTable(err) {
				continue
			}
			return fmt.Errorf("prune cache dims=%d: %w", dims, err)
		}
		if n > 0 {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  dims=%d: %d entries removed\n", dims, n)
		}
		total += n
	}
	if total == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "  (cache is empty)")
	} else {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Total: %d entries removed\n", total)
	}
	return nil
}

// isMissingTable returns true when the error indicates the cache table doesn't
// exist for a dimension — a normal situation when dimensions differ between
// the cache table set and commonDims.
func isMissingTable(err error) bool {
	return strings.Contains(err.Error(), "does not exist") ||
		strings.Contains(err.Error(), "no such table")
}
