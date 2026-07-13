package indexing

import (
	"bytes"
	"strings"
	"testing"
)

func TestMaybeWarnSQLiteScaleBelowThreshold(t *testing.T) {
	var buf bytes.Buffer
	MaybeWarnSQLiteScale(&buf, 100, 1000)
	if buf.Len() != 0 {
		t.Fatalf("unexpected warn: %q", buf.String())
	}
}

func TestMaybeWarnSQLiteScaleLargeChunks(t *testing.T) {
	var buf bytes.Buffer
	MaybeWarnSQLiteScale(&buf, 10, SQLiteWarnChunks)
	if !strings.Contains(buf.String(), "SQLite index is large") {
		t.Fatalf("expected scale warning, got %q", buf.String())
	}
}

func TestEffectiveMaxFiles(t *testing.T) {
	if got := effectiveMaxFiles(0, 0); got != 0 {
		t.Fatalf("0,0 = %d", got)
	}
	if got := effectiveMaxFiles(100, 50); got != 50 {
		t.Fatalf("100,50 = %d", got)
	}
	if got := effectiveMaxFiles(0, 25); got != 25 {
		t.Fatalf("0,25 = %d", got)
	}
}
