package store

import (
	"context"
	"errors"
	"testing"

	"github.com/lgldsilva/semidx/internal/tenant"
)

// Regression: CreateTenant ran three statements outside a transaction, so a
// failing membership insert left an orphan tenant behind.
func TestCreateTenantIsAtomic(t *testing.T) {
	s := newTestStore(t)
	slug := uniqueSlug(t, "atomic")
	ctx := tenant.MustWith(context.Background(), tenant.Context{ID: tenant.DefaultID, UserID: 987654})
	if _, err := s.CreateTenant(ctx, slug, "Atomic"); err == nil {
		t.Fatal("CreateTenant with a missing user should fail")
	}
	if _, err := s.GetTenantBySlug(context.Background(), slug); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tenant persisted after failed CreateTenant: err=%v", err)
	}
}
