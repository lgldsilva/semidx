package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/internal/tenant"
)

type authTenantStore struct {
	*fakeStore
	tenant store.Tenant
	allow  bool
}

type authWorkspaceStore struct {
	*authTenantStore
	workspace store.Workspace
}

func (f *authWorkspaceStore) GetWorkspaceBySlug(_ context.Context, slug string) (*store.Workspace, error) {
	if slug != f.workspace.Slug {
		return nil, store.ErrNotFound
	}
	return &f.workspace, nil
}

func (f *authWorkspaceStore) ListWorkspaces(context.Context) ([]store.Workspace, error) {
	return []store.Workspace{f.workspace}, nil
}

func (f *authWorkspaceStore) CreateWorkspace(context.Context, string, string) (*store.Workspace, error) {
	return &f.workspace, nil
}

func (f *authTenantStore) GetTenantBySlug(_ context.Context, slug string) (*store.Tenant, error) {
	if slug != f.tenant.Slug {
		return nil, store.ErrNotFound
	}
	return &f.tenant, nil
}

func (f *authTenantStore) CanAccessTenant(_ context.Context, _, tenantID int) (bool, error) {
	return f.allow && tenantID == f.tenant.ID, nil
}

func (f *authTenantStore) ListTenants(context.Context) ([]store.Tenant, error) {
	return []store.Tenant{f.tenant}, nil
}

func (f *authTenantStore) GetTenantByID(context.Context, int) (*store.Tenant, error) {
	return &f.tenant, nil
}

func (f *authTenantStore) CreateTenant(context.Context, string, string) (*store.Tenant, error) {
	return &f.tenant, nil
}

func (f *authTenantStore) ListMemberships(context.Context, int) ([]store.Membership, error) {
	return nil, nil
}

func (f *authTenantStore) UpsertMembership(context.Context, store.Membership) error {
	return nil
}

func TestAuthedAttachesTokenTenant(t *testing.T) {
	base := &fakeStore{token: &store.Token{Scopes: []string{"read"}, TenantID: 7}, project: &store.Project{Name: "p"}}
	srv := New(base, fakeEmbedder{}, nil)
	var got tenant.Context
	h := srv.authed("read", func(w http.ResponseWriter, r *http.Request) {
		got, _ = tenant.From(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || got.ID != 7 {
		t.Fatalf("status=%d tenant=%+v", rec.Code, got)
	}
}

func TestAuthedTenantHeaderRequiresMembership(t *testing.T) {
	base := &fakeStore{token: &store.Token{Scopes: []string{"read"}, TenantID: 1, UserID: 42}}
	other := &authTenantStore{
		fakeStore: base,
		tenant:    store.Tenant{ID: 2, Slug: "other", Name: "Other"},
	}
	srv := New(other, fakeEmbedder{}, nil)
	h := srv.authed("read", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("X-Semidx-Tenant", "other")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s, want forbidden without membership", rec.Code, rec.Body.String())
	}

	other.allow = true
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want access with membership", rec.Code, rec.Body.String())
	}
}

func TestAuthedAttachesWorkspaceSelector(t *testing.T) {
	base := &fakeStore{token: &store.Token{Scopes: []string{"read"}, TenantID: 1}}
	ws := &authWorkspaceStore{
		authTenantStore: &authTenantStore{fakeStore: base, tenant: store.Tenant{ID: 1, Slug: "default", Name: "Default"}},
		workspace:       store.Workspace{ID: 9, TenantID: 1, Slug: "platform", Name: "Platform"},
	}
	srv := New(ws, fakeEmbedder{}, nil)
	var got tenant.Context
	h := srv.authed("read", func(w http.ResponseWriter, r *http.Request) {
		got, _ = tenant.From(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("X-Semidx-Workspace", "platform")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || got.WorkspaceID != 9 || got.Workspace != "platform" {
		t.Fatalf("status=%d tenant=%+v", rec.Code, got)
	}
}
