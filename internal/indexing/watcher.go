// Package indexing — filesystem watcher for continuous re-indexing.
package indexing

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/lgldsilva/semidx/internal/chunker"
)

// Watcher watches a project directory tree for file changes and re-indexes
// them through the provided Indexer.
type Watcher struct {
	projectID   int
	projectPath string
	idx         *Indexer
	log         *slog.Logger
	model       string
	mu          sync.Mutex
}

// NewWatcher creates a Watcher bound to one project.
func NewWatcher(projectID int, projectPath, model string, idx *Indexer) *Watcher {
	return &Watcher{
		projectID:   projectID,
		projectPath: projectPath,
		model:       model,
		idx:         idx,
		log:         slog.Default(),
	}
}

// Watch starts the filesystem watcher and blocks until the context is
// cancelled. It debounces WRITE events with a 500ms window and re-indexes
// changed files via the existing Indexer. REMOVE events delete the file from
// the index. Directories listed in chunker.ignoredDirs are skipped.
func (w *Watcher) Watch(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("fsnotify watcher: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	// Walk the project tree and add all directories.
	if err := w.addDirs(watcher, w.projectPath); err != nil {
		return fmt.Errorf("add watch dirs: %w", err)
	}

	w.log.Info("watching for changes", "path", w.projectPath)

	// debounceTimers tracks per-path timers for WRITE event debouncing.
	debounceTimers := make(map[string]*time.Timer)
	const debounceWindow = 500 * time.Millisecond

	for {
		select {
		case <-ctx.Done():
			w.cancelTimers(debounceTimers)
			return ctx.Err()

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			w.handleEvent(ctx, watcher, event, debounceTimers, debounceWindow)

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			w.log.Warn("watcher error", "error", err)
		}
	}
}

// cancelTimers stops all pending debounce timers.
func (w *Watcher) cancelTimers(timers map[string]*time.Timer) {
	w.mu.Lock()
	for _, t := range timers {
		t.Stop()
	}
	w.mu.Unlock()
}

// handleEvent dispatches a single fsnotify event to the appropriate handler.
func (w *Watcher) handleEvent(ctx context.Context, watcher *fsnotify.Watcher, event fsnotify.Event, timers map[string]*time.Timer, window time.Duration) {
	if isIgnored(event.Name) {
		return
	}
	switch {
	case event.Op&fsnotify.Create != 0:
		w.handleCreateDir(ctx, watcher, event.Name)
	case event.Op&fsnotify.Write != 0:
		w.debounce(ctx, timers, event.Name, window)
	case event.Op&fsnotify.Remove != 0 || event.Op&fsnotify.Rename != 0:
		w.handleRemove(ctx, event.Name)
	}
}

// handleCreateDir starts watching a new directory (if applicable) and
// re-indexes the created file.
func (w *Watcher) handleCreateDir(ctx context.Context, watcher *fsnotify.Watcher, path string) {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		if !chunker.IsIgnoredDir(filepath.Base(path)) {
			_ = watcher.Add(path) // best-effort
		}
	}
	w.handleCreate(ctx, path)
}

// addDirs recursively adds all directories under root to the watcher.
func (w *Watcher) addDirs(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && chunker.IsIgnoredDir(d.Name()) {
			return filepath.SkipDir
		}
		return watcher.Add(path)
	})
}

// isIgnored reports whether a watched path falls inside an ignored directory.
func isIgnored(path string) bool {
	parts := strings.Split(path, string(filepath.Separator))
	for _, part := range parts {
		if chunker.IsIgnoredDir(part) {
			return true
		}
	}
	return false
}

// debounce queues a re-index for path after the debounce window, cancelling
// any previously queued timer for the same path.
func (w *Watcher) debounce(ctx context.Context, timers map[string]*time.Timer, path string, window time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if t, ok := timers[path]; ok {
		t.Stop()
	}
	timers[path] = time.AfterFunc(window, func() {
		w.mu.Lock()
		delete(timers, path)
		w.mu.Unlock()
		w.handleCreate(ctx, path)
	})
}

// handleCreate re-indexes a single changed file.
func (w *Watcher) handleCreate(ctx context.Context, path string) {
	rel, err := filepath.Rel(w.projectPath, path)
	if err != nil {
		rel = path
	}

	if !chunker.ShouldIndex(rel) {
		return // not an indexable file
	}

	// indexFile reads the file from disk — no need to read it here.
	_, softErrs, outcome, _, ferr := w.idx.indexFile(ctx, w.projectID, path, rel, w.model)
	if ferr != nil {
		w.log.Warn("watcher index", "path", rel, "error", ferr)
		return
	}
	if softErrs > 0 {
		w.log.Warn("watcher index soft errors", "path", rel, "count", softErrs)
	}
	switch outcome {
	case outcomeIndexed:
		w.log.Info("watcher indexed", "path", rel)
	case outcomeSkippedUnchanged:
		// No change needed.
	case outcomeSkippedEmpty:
		w.log.Debug("watcher skipped (empty)", "path", rel)
	}
}

// handleRemove deletes a file from the index.
func (w *Watcher) handleRemove(ctx context.Context, path string) {
	rel, err := filepath.Rel(w.projectPath, path)
	if err != nil {
		rel = path
	}

	if err := w.idx.db.DeleteFileByPath(ctx, w.projectID, rel); err != nil {
		w.log.Warn("watcher remove", "path", rel, "error", err)
		return
	}
	w.log.Info("watcher removed", "path", rel)
}
