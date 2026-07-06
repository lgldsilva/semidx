package localstore_test

import (
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/indexstoretest"
	"github.com/lgldsilva/semidx/internal/localstore"
	"github.com/lgldsilva/semidx/internal/store"
)

func TestIndexStoreConformanceSQLite(t *testing.T) {
	indexstoretest.Run(t, func(t *testing.T) store.IndexStore {
		t.Helper()
		s, err := localstore.New(filepath.Join(t.TempDir(), "idx.db"))
		if err != nil {
			t.Fatalf("open sqlite: %v", err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	})
}
