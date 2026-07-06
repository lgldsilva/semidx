package store_test

import (
	"testing"

	"github.com/lgldsilva/semidx/internal/indexstoretest"
	"github.com/lgldsilva/semidx/internal/store"
)

func TestIndexStoreConformancePostgres(t *testing.T) {
	indexstoretest.Run(t, func(t *testing.T) store.IndexStore {
		t.Helper()
		return store.NewTestPgStore(t)
	})
}
