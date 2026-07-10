package extract

import (
	"bytes"
	"testing"
	"time"
)

// TestExtractCorruptTarTerminates is a regression test for the infinite loop
// where a non-EOF error from tar.Reader.Next() (which the reader memoises) was
// met with "continue", spinning forever on a corrupt archive.
func TestExtractCorruptTarTerminates(t *testing.T) {
	corrupt := bytes.Repeat([]byte{0xAA}, 2048) // not a valid tar header

	done := make(chan struct{})
	go func() {
		_, _ = extractGenericArchive("bad.tar", corrupt)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("extractGenericArchive hung on a corrupt tar (A1 regression)")
	}
}
