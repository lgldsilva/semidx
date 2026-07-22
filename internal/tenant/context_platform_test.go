package tenant

import (
	"context"
	"testing"
)

func TestMustWithAttachesValidatedContext(t *testing.T) {
	ctx := MustWith(context.Background(), Context{ID: 9, Slug: "acme", WorkspaceID: 4})
	got, ok := From(ctx)
	if !ok || got.ID != 9 || got.WorkspaceID != 4 {
		t.Fatalf("MustWith context = %+v, ok=%v", got, ok)
	}
}
