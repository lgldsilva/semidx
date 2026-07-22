package tenant

import (
	"context"
	"testing"
)

func TestContextRoundTripAndDefault(t *testing.T) {
	if got := ID(context.Background()); got != DefaultID {
		t.Fatalf("default tenant = %d, want %d", got, DefaultID)
	}
	ctx, err := With(context.Background(), Context{ID: 7, Slug: "acme", UserID: 3})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := From(ctx)
	if !ok || got.ID != 7 || got.Slug != "acme" || got.UserID != 3 {
		t.Fatalf("context = %+v, ok=%v", got, ok)
	}
	if id := ID(ctx); id != 7 {
		t.Fatalf("tenant ID = %d, want 7", id)
	}
}

func TestWithRejectsInvalidID(t *testing.T) {
	if _, err := With(context.Background(), Context{}); err == nil {
		t.Fatal("With should reject a non-positive tenant ID")
	}
	if _, err := Require(context.Background()); err == nil {
		t.Fatal("Require should reject an unscoped context")
	}
}
