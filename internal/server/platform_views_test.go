package server

import (
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func TestTenantAndWorkspaceViews(t *testing.T) {
	tenantView := toTenantView(store.Tenant{ID: 7, Slug: "acme", Name: "Acme"})
	if tenantView.ID != 7 || tenantView.Slug != "acme" || tenantView.Name != "Acme" {
		t.Fatalf("tenant view = %+v", tenantView)
	}
	workspaceView := toWorkspaceView(store.Workspace{ID: 8, TenantID: 7, Slug: "platform", Name: "Platform"})
	if workspaceView.ID != 8 || workspaceView.TenantID != 7 || workspaceView.Slug != "platform" {
		t.Fatalf("workspace view = %+v", workspaceView)
	}
	if _, ok := tenantStore(&Server{}); ok {
		t.Error("tenantStore should report unavailable for a server without a store")
	}
	if _, ok := workspaceStore(&Server{}); ok {
		t.Error("workspaceStore should report unavailable for a server without a store")
	}
}
