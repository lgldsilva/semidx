package search

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/lgldsilva/semidx/internal/store"
)

// annotateStaleness marks each result whose on-disk content no longer matches
// the hash captured at index time. Best-effort: never fails the search. When
// the project has no local path (remote-only server project) or a file cannot
// be read, Stale stays false and IndexedAt may still be filled from the store.
//
// Each hit is compared against its own file version (SearchResult.FileHash),
// not the project's most recent version of the path: with several worktrees
// indexed, a path can hold divergent versions and only the hit's version says
// whether the checkout under root changed. Hits from stores that do not report
// a FileHash fall back to the project-wide latest hash per path.
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

	var infos map[string]store.FileHashInfo // lazily loaded fallback
	infosLoaded := false
	// Hash each unique hit path once (topK is small).
	current := make(map[string]string, len(results))
	for i := range results {
		r := &results[i]
		hash := r.FileHash
		if hash == "" {
			if !infosLoaded {
				infos = loadFileHashInfos(ctx, s.store, project)
				infosLoaded = true
			}
			info, ok := infos[r.FilePath]
			if !ok {
				continue
			}
			hash = info.Hash
			if r.IndexedAt.IsZero() {
				r.IndexedAt = info.IndexedAt
			}
		}
		cur, seen := current[r.FilePath]
		if !seen {
			cur = hashProjectFile(root, r.FilePath)
			current[r.FilePath] = cur
		}
		if cur == "" || hash == "" {
			continue
		}
		r.Stale = cur != hash
	}
}

// loadFileHashInfos is the best-effort fallback lookup for hits without a
// FileHash; any failure yields nil (no staleness, search continues).
func loadFileHashInfos(ctx context.Context, db store.IndexStore, project *store.Project) map[string]store.FileHashInfo {
	infos, err := listFileHashInfos(ctx, db, project.ID)
	if err != nil {
		slog.Debug("staleness: list file hashes failed", "project", project.Name, "err", err)
		return nil
	}
	return infos
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
// cannot be read or rel escapes the project root. The file is hashed as a
// stream, so memory stays flat regardless of the file size (a hit on a
// multi-GB file must not load it into RAM).
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
	f, err := os.Open(full) // #nosec G304 -- path confined to project root above
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	// codeql[go/weak-sensitive-data-hashing] : content-addressed digest, same
	// as indexing.ContentHash, not a security control.
	return hex.EncodeToString(h.Sum(nil))
}
