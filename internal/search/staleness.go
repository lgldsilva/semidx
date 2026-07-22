package search

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/store"
)

// annotateStaleness marks each result whose on-disk content no longer matches
// the hash captured at index time. Best-effort: never fails the search. When
// the project has no local path (remote-only server project) or a file cannot
// be read, Stale stays false and IndexedAt may still be filled from the store.
//
// root, when non-empty (e.g. a worktree path), is preferred over project.Path.
func (s *Service) annotateStaleness(ctx context.Context, project *store.Project, root string, results []store.SearchResult) {
	if project == nil || len(results) == 0 {
		return
	}
	if root == "" {
		root = project.Path
	}
	if root == "" {
		return
	}

	infos, err := listFileHashInfos(ctx, s.store, project.ID)
	if err != nil {
		slog.Debug("staleness: list file hashes failed", "project", project.Name, "err", err)
		return
	}
	if infos == nil {
		return
	}

	// Hash each unique hit path once (topK is small).
	current := make(map[string]string, len(results))
	for i := range results {
		path := results[i].FilePath
		info, ok := infos[path]
		if ok {
			results[i].IndexedAt = info.IndexedAt
		}
		cur, seen := current[path]
		if !seen {
			cur = hashProjectFile(root, path)
			current[path] = cur
		}
		if cur == "" || !ok || info.Hash == "" {
			continue
		}
		results[i].Stale = cur != info.Hash
	}
}

// listFileHashInfos loads path→hash(+indexed_at). Incomplete test doubles that
// embed a nil IndexStore would panic on the new method; recover turns that into
// a soft skip so search never fails because of staleness plumbing.
func listFileHashInfos(ctx context.Context, db store.IndexStore, projectID int) (infos map[string]store.FileHashInfo, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Debug("staleness: ListFileHashesWithTime panicked", "recover", rec)
			infos, err = nil, nil
		}
	}()
	return db.ListFileHashesWithTime(ctx, projectID)
}

// hashProjectFile returns the content hash of root/rel, or "" when the file
// cannot be read or rel escapes the project root.
func hashProjectFile(root, rel string) string {
	if root == "" || rel == "" {
		return ""
	}
	cleanRoot := filepath.Clean(root)
	full := filepath.Clean(filepath.Join(cleanRoot, rel))
	// Reject path traversal outside the project root.
	sep := string(filepath.Separator)
	if full != cleanRoot && !strings.HasPrefix(full, cleanRoot+sep) {
		slog.Debug("staleness: path escapes project root", "root", cleanRoot, "path", rel)
		return ""
	}
	data, err := os.ReadFile(full) // #nosec G304 -- path confined to project root above
	if err != nil {
		return ""
	}
	return indexing.ContentHash(data)
}
