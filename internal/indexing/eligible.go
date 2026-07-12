package indexing

import (
	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/extract"
)

// Eligible reports whether a relative file path should be indexed: it either
// passes chunker.ShouldIndex OR is supported by the extract registry.
// Keeps the indexer (ScanFiles) and watcher (handleCreate) in sync, so the
// watcher bug that only checked chunker.ShouldIndex never recurs.
func Eligible(rel string) bool {
	return chunker.ShouldIndex(rel) || extract.Supported(rel)
}
