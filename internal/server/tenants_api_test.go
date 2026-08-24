package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

// tenantAPIStore implements the optional TenantStore + WorkspaceStore surfaces
// with injectable errors. A bare *fakeStore implements neither, which is how
// the "requires PostgreSQL" 501 paths are covered.
type tenantAPIStore struct {
	*fakeStore
	tenants    []store.Tenant
	workspaces []store.Workspace
	created    *store.Tenant
	createdWS  *store.Workspace
	listErr    error
	createErrT error
	listWSErr  error
	createWSMu error
}

func (s *tenantAPIStore) ListTenants(context.Context) ([]store.Tenant, error) {
	return s.tenants, s.listErr
}

func (s *tenantAPIStore) GetTenantByID(_ context.Context, id int) (*store.Tenant, error) {
	return &store.Tenant{ID: id, Slug: "acme", Name: "ACME"}, nil
}

func (s *tenantAPIStore) GetTenantBySlug(_ context.Context, slug string) (*store.Tenant, error) {
	return &store.Tenant{ID: 1, Slug: slug, Name: slug}, nil
}

func (s *tenantAPIStore) CreateTenant(_ context.Context, slug, name string) (*store.Tenant, error) {
	if s.createErrT != nil {
		return nil, s.createErrT
	}
	s.created = &store.Tenant{ID: 7, Slug: slug, Name: name}
	return s.created, nil
}

func (s *tenantAPIStore) ListMemberships(context.Context, int) ([]store.Membership, error) {
	return nil, nil
}

func (s *tenantAPIStore) UpsertMembership(context.Context, store.Membership) error { return nil }

func (s *tenantAPIStore) CanAccessTenant(context.Context, int, int) (bool, error) {
	return true, nil
}

func (s *tenantAPIStore) ListWorkspaces(context.Context) ([]store.Workspace, error) {
	return s.workspaces, s.listWSErr
}

// GetWorkspaceBySlug always resolves: authed() selects the "default" workspace
// when no X-Semidx-Workspace header is sent, and a miss there would answer 403
// before any of the handlers under test ran.
func (s *tenantAPIStore) GetWorkspaceBySlug(_ context.Context, slug string) (*store.Workspace, error) {
	return &store.Workspace{ID: 1, TenantID: 1, Slug: slug, Name: slug}, nil
}

func (s *tenantAPIStore) CreateWorkspace(_ context.Context, slug, name string) (*store.Workspace, error) {
	if s.createWSMu != nil {
		return nil, s.createWSMu
	}
	s.createdWS = &store.Workspace{ID: 11, TenantID: 1, Slug: slug, Name: name}
	return s.createdWS, nil
}

func newTenantAPIStore(scopes ...string) *tenantAPIStore {
	return &tenantAPIStore{fakeStore: &fakeStore{token: &store.Token{Scopes: scopes, TenantID: 1}}}
}

func TestListTenants(t *testing.T) {
	st := newTenantAPIStore("read")
	st.tenants = []store.Tenant{{ID: 1, Slug: "acme", Name: "ACME"}}

	rec := do(t, New(st, fakeEmbedder{}, nil), "GET", "/api/v1/tenants", "tok", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list tenants = %d, body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Tenants []tenantView `json:"tenants"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if len(out.Tenants) != 1 || out.Tenants[0].Slug != "acme" || out.Tenants[0].Name != "ACME" {
		t.Errorf("tenants = %+v", out.Tenants)
	}
}

func TestListTenantsErrors(t *testing.T) {
	plain := &fakeStore{token: &store.Token{Scopes: []string{"read"}}}
	if rec := do(t, New(plain, fakeEmbedder{}, nil), "GET", "/api/v1/tenants", "tok", ""); rec.Code != http.StatusNotImplemented {
		t.Errorf("store without tenants = %d, want 501", rec.Code)
	}

	broken := newTenantAPIStore("read")
	broken.listErr = errors.New("db down")
	if rec := do(t, New(broken, fakeEmbedder{}, nil), "GET", "/api/v1/tenants", "tok", ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("list failure = %d, want 500", rec.Code)
	}
}

func TestCreateTenant(t *testing.T) {
	st := newTenantAPIStore("admin")
	rec := do(t, New(st, fakeEmbedder{}, nil), "POST", "/api/v1/tenants", "tok", `{"slug":" ACME ","name":" ACME Inc "}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create tenant = %d, body %s", rec.Code, rec.Body.String())
	}
	if st.created == nil || st.created.Slug != "acme" || st.created.Name != "ACME Inc" {
		t.Errorf("stored tenant = %+v (slug must be lowercased and trimmed)", st.created)
	}
}

func TestCreateTenantValidation(t *testing.T) {
	srv := New(newTenantAPIStore("admin"), fakeEmbedder{}, nil)

	cases := []struct{ name, body string }{
		{"invalid JSON", "not json"},
		{"slug too short", `{"slug":"a","name":"n"}`},
		{"slug with underscore", `{"slug":"ac_me","name":"n"}`},
		{"slug starting with hyphen", `{"slug":"-acme","name":"n"}`},
		{"empty name", `{"slug":"acme","name":"  "}`},
		{"name too long", `{"slug":"acme","name":"` + strings.Repeat("x", 256) + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := do(t, srv, "POST", "/api/v1/tenants", "tok", tc.body); rec.Code != http.StatusBadRequest {
				t.Errorf("%s = %d, want 400", tc.name, rec.Code)
			}
		})
	}
}

func TestCreateTenantStoreErrors(t *testing.T) {
	plain := &fakeStore{token: &store.Token{Scopes: []string{"admin"}}}
	if rec := do(t, New(plain, fakeEmbedder{}, nil), "POST", "/api/v1/tenants", "tok", `{"slug":"acme","name":"n"}`); rec.Code != http.StatusNotImplemented {
		t.Errorf("store without tenants = %d, want 501", rec.Code)
	}

	dup := newTenantAPIStore("admin")
	dup.createErrT = store.ErrTenantExists
	if rec := do(t, New(dup, fakeEmbedder{}, nil), "POST", "/api/v1/tenants", "tok", `{"slug":"acme","name":"n"}`); rec.Code != http.StatusConflict {
		t.Errorf("duplicate tenant = %d, want 409", rec.Code)
	}

	broken := newTenantAPIStore("admin")
	broken.createErrT = errors.New("db down")
	if rec := do(t, New(broken, fakeEmbedder{}, nil), "POST", "/api/v1/tenants", "tok", `{"slug":"acme","name":"n"}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("create failure = %d, want 500", rec.Code)
	}
}

func TestListWorkspaces(t *testing.T) {
	st := newTenantAPIStore("read")
	st.workspaces = []store.Workspace{{ID: 9, TenantID: 1, Slug: "platform", Name: "Platform"}}

	rec := do(t, New(st, fakeEmbedder{}, nil), "GET", "/api/v1/workspaces", "tok", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list workspaces = %d, body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Workspaces []workspaceView `json:"workspaces"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if len(out.Workspaces) != 1 || out.Workspaces[0].Slug != "platform" || out.Workspaces[0].TenantID != 1 {
		t.Errorf("workspaces = %+v", out.Workspaces)
	}
}

func TestListWorkspacesErrors(t *testing.T) {
	plain := &fakeStore{token: &store.Token{Scopes: []string{"read"}}}
	if rec := do(t, New(plain, fakeEmbedder{}, nil), "GET", "/api/v1/workspaces", "tok", ""); rec.Code != http.StatusNotImplemented {
		t.Errorf("store without workspaces = %d, want 501", rec.Code)
	}

	broken := newTenantAPIStore("read")
	broken.listWSErr = errors.New("db down")
	if rec := do(t, New(broken, fakeEmbedder{}, nil), "GET", "/api/v1/workspaces", "tok", ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("list failure = %d, want 500", rec.Code)
	}
}

func TestCreateWorkspace(t *testing.T) {
	st := newTenantAPIStore("admin")
	rec := do(t, New(st, fakeEmbedder{}, nil), "POST", "/api/v1/workspaces", "tok", `{"slug":"Platform","name":" Platform "}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create workspace = %d, body %s", rec.Code, rec.Body.String())
	}
	if st.createdWS == nil || st.createdWS.Slug != "platform" || st.createdWS.Name != "Platform" {
		t.Errorf("stored workspace = %+v", st.createdWS)
	}
}

func TestCreateWorkspaceValidation(t *testing.T) {
	srv := New(newTenantAPIStore("admin"), fakeEmbedder{}, nil)

	cases := []struct{ name, body string }{
		{"invalid JSON", "not json"},
		{"slug too short", `{"slug":"p","name":"n"}`},
		{"slug with dot", `{"slug":"plat.form","name":"n"}`},
		{"empty name", `{"slug":"platform","name":""}`},
		{"name too long", `{"slug":"platform","name":"` + strings.Repeat("x", 256) + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := do(t, srv, "POST", "/api/v1/workspaces", "tok", tc.body); rec.Code != http.StatusBadRequest {
				t.Errorf("%s = %d, want 400", tc.name, rec.Code)
			}
		})
	}
}

func TestCreateWorkspaceStoreErrors(t *testing.T) {
	plain := &fakeStore{token: &store.Token{Scopes: []string{"admin"}}}
	if rec := do(t, New(plain, fakeEmbedder{}, nil), "POST", "/api/v1/workspaces", "tok", `{"slug":"platform","name":"n"}`); rec.Code != http.StatusNotImplemented {
		t.Errorf("store without workspaces = %d, want 501", rec.Code)
	}

	dup := newTenantAPIStore("admin")
	dup.createWSMu = store.ErrWorkspaceExists
	if rec := do(t, New(dup, fakeEmbedder{}, nil), "POST", "/api/v1/workspaces", "tok", `{"slug":"platform","name":"n"}`); rec.Code != http.StatusConflict {
		t.Errorf("duplicate workspace = %d, want 409", rec.Code)
	}

	broken := newTenantAPIStore("admin")
	broken.createWSMu = errors.New("db down")
	if rec := do(t, New(broken, fakeEmbedder{}, nil), "POST", "/api/v1/workspaces", "tok", `{"slug":"platform","name":"n"}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("create failure = %d, want 500", rec.Code)
	}
}
