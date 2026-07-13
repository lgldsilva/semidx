package indexing

import (
	"fmt"
	"io"
)

// SQLite scale guidance thresholds (see docs/research/large-scale-semantic-code-search.md).
const (
	SQLiteWarnFiles  = 100_000
	SQLiteWarnChunks = 500_000
)

// MaybeWarnSQLiteScale prints a stderr warning when a local SQLite index exceeds
// recommended size. No-op when files and chunks are both below thresholds.
func MaybeWarnSQLiteScale(w io.Writer, filesIndexed, chunksCreated int) {
	if w == nil {
		return
	}
	if filesIndexed < SQLiteWarnFiles && chunksCreated < SQLiteWarnChunks {
		return
	}
	_, _ = fmt.Fprintf(w, "[warn] SQLite index is large (%d files, %d chunks). "+
		"Search latency grows linearly with corpus size — consider `semidx serve` with Postgres/pgvector "+
		"for shared indexes above ~50k chunks.\n", filesIndexed, chunksCreated)
}
