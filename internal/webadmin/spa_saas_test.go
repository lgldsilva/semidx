package webadmin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

// saasAdminStore adds the optional product surfaces (dependency catalog,
// runtime graph, quotas, privacy policies, tenants/workspaces) to the admin
// fake store. The plain fakeStore implements none of them, which is how the
// "unavailable" branches stay covered.
type saasAdminStore struct {
	*fakeStore
	deps       []store.Dependency
	shared     []store.DependencyUsage
	edges      []store.RuntimeEdge
	tenants    []store.Tenant
	workspaces []store.Workspace
	quota      store.TenantQuota
	usage      store.TenantUsage
	privacy    string

	depsErr    error
	sharedErr  error
	edgesErr   error
	wsEdgesErr error
	quotaErr   error
	usageErr   error
	tenantsErr error
	wsErr      error
	privacyErr error
}

func newSaaSAdminStore() *saasAdminStore {
	return &saasAdminStore{fakeStore: newFakeStore()}
}

func (s *saasAdminStore) ListProjectDependencies(context.Context, int) ([]store.Dependency, error) {
	return s.deps, s.depsErr
}

func (s *saasAdminStore) FindProjectsSharingDependency(context.Context, int) ([]store.DependencyUsage, error) {
	return s.shared, s.sharedErr
}

func (s *saasAdminStore) ReplaceProjectDependencies(context.Context, int, []store.Dependency) error {
	return nil
}

func (s *saasAdminStore) UpsertRuntimeEdges(context.Context, int, []store.RuntimeEdge) error {
	return nil
}

func (s *saasAdminStore) ListRuntimeEdges(context.Context, int) ([]store.RuntimeEdge, error) {
	return s.edges, s.edgesErr
}

func (s *saasAdminStore) ListWorkspaceRuntimeEdges(context.Context, int) ([]store.RuntimeEdge, error) {
	return s.edges, s.wsEdgesErr
}

func (s *saasAdminStore) GetTenantQuota(context.Context) (*store.TenantQuota, error) {
	if s.quotaErr != nil {
		return nil, s.quotaErr
	}
	return &s.quota, nil
}

func (s *saasAdminStore) SetTenantQuota(context.Context, store.TenantQuota) error { return nil }

func (s *saasAdminStore) GetTenantUsage(context.Context) (*store.TenantUsage, error) {
	if s.usageErr != nil {
		return nil, s.usageErr
	}
	return &s.usage, nil
}

func (s *saasAdminStore) SetProjectPrivacy(_ context.Context, _ int, mode string) error {
	if s.privacyErr != nil {
		return s.privacyErr
	}
	s.privacy = mode
	return nil
}

func (s *saasAdminStore) ListTenants(context.Context) ([]store.Tenant, error) {
	return s.tenants, s.tenantsErr
}

func (s *saasAdminStore) GetTenantByID(_ context.Context, id int) (*store.Tenant, error) {
	return &store.Tenant{ID: id, Slug: "default", Name: "Default"}, nil
}

func (s *saasAdminStore) GetTenantBySlug(_ context.Context, slug string) (*store.Tenant, error) {
	return &store.Tenant{ID: 1, Slug: slug, Name: slug}, nil
}

func (s *saasAdminStore) CreateTenant(_ context.Context, slug, name string) (*store.Tenant, error) {
	return &store.Tenant{ID: 2, Slug: slug, Name: name}, nil
}

func (s *saasAdminStore) ListMemberships(context.Context, int) ([]store.Membership, error) {
	return nil, nil
}

func (s *saasAdminStore) UpsertMembership(context.Context, store.Membership) error { return nil }

func (s *saasAdminStore) CanAccessTenant(context.Context, int, int) (bool, error) {
	return true, nil
}

func (s *saasAdminStore) ListWorkspaces(context.Context) ([]store.Workspace, error) {
	return s.workspaces, s.wsErr
}

func (s *saasAdminStore) GetWorkspaceBySlug(_ context.Context, slug string) (*store.Workspace, error) {
	return &store.Workspace{ID: 1, TenantID: 1, Slug: slug, Name: slug}, nil
}

func (s *saasAdminStore) CreateWorkspace(_ context.Context, slug, name string) (*store.Workspace, error) {
	return &store.Workspace{ID: 2, TenantID: 1, Slug: slug, Name: name}, nil
}

// newSaaSAdmin logs an admin in against a store of the caller's choosing and
// returns the server plus an authenticated client and its CSRF token.
func newSaaSAdmin(t *testing.T, st store.Store, seed func()) (*httptest.Server, *http.Client, string) {
	t.Helper()
	seed()
	a, err := New(st, fakeEmbedder{}, nil, true, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(a.Handler())
	t.Cleanup(srv.Close)
	c := newClient(t, srv)
	login(t, c, srv.URL, "root", "supersecret")
	return srv, c, csrfFrom(t, c, srv.URL+"/admin/keys")
}

func saasAdmin(t *testing.T, st *saasAdminStore) (*httptest.Server, *http.Client, string) {
	t.Helper()
	return newSaaSAdmin(t, st, func() {
		st.addUser("root", "supersecret", "admin")
		st.projects = append(st.projects, store.Project{ID: 1, Name: "repo", Model: "bge-m3"})
	})
}

func plainAdmin(t *testing.T) (*httptest.Server, *http.Client, string) {
	t.Helper()
	fs := newFakeStore()
	return newSaaSAdmin(t, fs, func() {
		fs.addUser("root", "supersecret", "admin")
		fs.projects = append(fs.projects, store.Project{ID: 1, Name: "repo", Model: "bge-m3"})
	})
}

func TestAdminProjectDependencies(t *testing.T) {
	st := newSaaSAdminStore()
	st.deps = []store.Dependency{{Ecosystem: "go", Name: "github.com/x/y", ResolvedVersion: "v1.2.3"}}
	st.shared = []store.DependencyUsage{{ProjectName: "other", Name: "github.com/x/y"}}
	srv, c, _ := saasAdmin(t, st)

	code, body := getBody(t, c, srv.URL+"/admin/api/projects/repo/dependencies")
	if code != http.StatusOK || !strings.Contains(body, "github.com/x/y") {
		t.Fatalf("dependencies = %d %s", code, body)
	}
	code, body = getBody(t, c, srv.URL+"/admin/api/projects/repo/dependencies/shared")
	if code != http.StatusOK || !strings.Contains(body, "other") {
		t.Fatalf("shared dependencies = %d %s", code, body)
	}
}

func TestAdminRuntimeGraphRoutes(t *testing.T) {
	st := newSaaSAdminStore()
	st.edges = []store.RuntimeEdge{{TargetProjectName: "other", Protocol: "http"}}
	srv, c, _ := saasAdmin(t, st)

	if code, body := getBody(t, c, srv.URL+"/admin/api/projects/repo/runtime-edges"); code != http.StatusOK || !strings.Contains(body, "other") {
		t.Fatalf("project runtime edges = %d %s", code, body)
	}
	if code, body := getBody(t, c, srv.URL+"/admin/api/runtime-graph"); code != http.StatusOK || !strings.Contains(body, "other") {
		t.Fatalf("runtime graph = %d %s", code, body)
	}
}

// The project-scoped SaaS reads share one failure contract: a store without the
// optional surface answers 501, a store whose query fails answers 500, and an
// unknown project answers 404. Table-driven so all four routes stay in lockstep.
func TestAdminSaaSReadFailureContract(t *testing.T) {
	routes := []struct {
		name     string
		path     string
		breakSt  func(*saasAdminStore)
		hasGhost bool
	}{
		{"dependencies", "/admin/api/projects/repo/dependencies",
			func(s *saasAdminStore) { s.depsErr = errors.New("db down") }, true},
		{"shared dependencies", "/admin/api/projects/repo/dependencies/shared",
			func(s *saasAdminStore) { s.sharedErr = errors.New("db down") }, true},
		{"project runtime edges", "/admin/api/projects/repo/runtime-edges",
			func(s *saasAdminStore) { s.edgesErr = errors.New("db down") }, true},
		{"portfolio runtime graph", "/admin/api/runtime-graph",
			func(s *saasAdminStore) { s.wsEdgesErr = errors.New("db down") }, false},
	}
	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			plain, pc, _ := plainAdmin(t)
			if code, _ := getBody(t, pc, plain.URL+rt.path); code != http.StatusNotImplemented {
				t.Errorf("store without the surface = %d, want 501", code)
			}

			st := newSaaSAdminStore()
			rt.breakSt(st)
			srv, c, _ := saasAdmin(t, st)
			if code, _ := getBody(t, c, srv.URL+rt.path); code != http.StatusInternalServerError {
				t.Errorf("query failure = %d, want 500", code)
			}
			if rt.hasGhost {
				ghost := strings.Replace(rt.path, "/repo/", "/ghost/", 1)
				if code, _ := getBody(t, c, srv.URL+ghost); code != http.StatusNotFound {
					t.Errorf("missing project = %d, want 404", code)
				}
			}
		})
	}
}

func TestAdminUsageRoute(t *testing.T) {
	st := newSaaSAdminStore()
	st.quota = store.TenantQuota{MaxProjects: 10, MaxRuntimeEdges: 100}
	st.usage = store.TenantUsage{Projects: 2, RuntimeEdges: 5}
	srv, c, _ := saasAdmin(t, st)

	if code, body := getBody(t, c, srv.URL+"/admin/api/usage"); code != http.StatusOK || !strings.Contains(body, "quota") {
		t.Fatalf("usage = %d %s", code, body)
	}

	plain, pc, _ := plainAdmin(t)
	if code, _ := getBody(t, pc, plain.URL+"/admin/api/usage"); code != http.StatusNotImplemented {
		t.Errorf("store without quotas = %d, want 501", code)
	}

	quotaBroken := newSaaSAdminStore()
	quotaBroken.quotaErr = errors.New("db down")
	srvQ, cQ, _ := saasAdmin(t, quotaBroken)
	if code, _ := getBody(t, cQ, srvQ.URL+"/admin/api/usage"); code != http.StatusInternalServerError {
		t.Errorf("quota failure = %d, want 500", code)
	}

	usageBroken := newSaaSAdminStore()
	usageBroken.usageErr = errors.New("db down")
	srvU, cU, _ := saasAdmin(t, usageBroken)
	if code, _ := getBody(t, cU, srvU.URL+"/admin/api/usage"); code != http.StatusInternalServerError {
		t.Errorf("usage failure = %d, want 500", code)
	}
}

func TestAdminSearchUsageRoute(t *testing.T) {
	srv, c, _ := plainAdmin(t)
	if code, body := getBody(t, c, srv.URL+"/admin/api/search-usage"); code != http.StatusOK {
		t.Fatalf("search usage = %d %s", code, body)
	}
	if code, _ := getBody(t, c, srv.URL+"/admin/api/search-usage?days=7&project=repo"); code != http.StatusOK {
		t.Errorf("windowed search usage = %d, want 200", code)
	}
}

func TestAdminProjectPrivacyRoute(t *testing.T) {
	st := newSaaSAdminStore()
	srv, c, csrf := saasAdmin(t, st)

	code, body := putAdminJSON(t, c, srv.URL+"/admin/api/projects/repo/privacy", csrf, map[string]any{"mode": "edge"})
	if code != http.StatusOK {
		t.Fatalf("set privacy = %d %s", code, body)
	}
	if st.privacy != "edge" {
		t.Errorf("stored mode = %q, want edge", st.privacy)
	}

	if code, _ := putAdminJSON(t, c, srv.URL+"/admin/api/projects/repo/privacy", csrf, map[string]any{"mode": "nonsense"}); code != http.StatusBadRequest {
		t.Errorf("unknown mode = %d, want 400", code)
	}
	if code, _ := putAdminJSON(t, c, srv.URL+"/admin/api/projects/ghost/privacy", csrf, map[string]any{"mode": "edge"}); code != http.StatusNotFound {
		t.Errorf("missing project = %d, want 404", code)
	}

	plain, pc, pcsrf := plainAdmin(t)
	if code, _ := putAdminJSON(t, pc, plain.URL+"/admin/api/projects/repo/privacy", pcsrf, map[string]any{"mode": "edge"}); code != http.StatusNotImplemented {
		t.Errorf("store without policies = %d, want 501", code)
	}

	broken := newSaaSAdminStore()
	broken.privacyErr = errors.New("db down")
	srvB, cB, csrfB := saasAdmin(t, broken)
	if code, _ := putAdminJSON(t, cB, srvB.URL+"/admin/api/projects/repo/privacy", csrfB, map[string]any{"mode": "edge"}); code != http.StatusInternalServerError {
		t.Errorf("save failure = %d, want 500", code)
	}
}

func TestAdminTenantAndWorkspaceRoutes(t *testing.T) {
	st := newSaaSAdminStore()
	st.tenants = []store.Tenant{{ID: 1, Slug: "acme", Name: "ACME"}}
	st.workspaces = []store.Workspace{{ID: 9, TenantID: 1, Slug: "platform", Name: "Platform"}}
	srv, c, _ := saasAdmin(t, st)

	if code, body := getBody(t, c, srv.URL+"/admin/api/tenants"); code != http.StatusOK || !strings.Contains(body, "acme") {
		t.Fatalf("tenants = %d %s", code, body)
	}
	if code, body := getBody(t, c, srv.URL+"/admin/api/workspaces"); code != http.StatusOK || !strings.Contains(body, "platform") {
		t.Fatalf("workspaces = %d %s", code, body)
	}

	// A store without the SaaS surfaces still answers with the single implicit
	// "default" tenant and no workspaces, so the SPA renders either way.
	plain, pc, _ := plainAdmin(t)
	if code, body := getBody(t, pc, plain.URL+"/admin/api/tenants"); code != http.StatusOK || !strings.Contains(body, "default") {
		t.Errorf("fallback tenants = %d %s", code, body)
	}
	if code, body := getBody(t, pc, plain.URL+"/admin/api/workspaces"); code != http.StatusOK || !strings.Contains(body, "workspaces") {
		t.Errorf("fallback workspaces = %d %s", code, body)
	}

	broken := newSaaSAdminStore()
	broken.tenantsErr = errors.New("db down")
	broken.wsErr = errors.New("db down")
	srvB, cB, _ := saasAdmin(t, broken)
	if code, _ := getBody(t, cB, srvB.URL+"/admin/api/tenants"); code != http.StatusInternalServerError {
		t.Errorf("tenants failure = %d, want 500", code)
	}
	if code, _ := getBody(t, cB, srvB.URL+"/admin/api/workspaces"); code != http.StatusInternalServerError {
		t.Errorf("workspaces failure = %d, want 500", code)
	}
}
