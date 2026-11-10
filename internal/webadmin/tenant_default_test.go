package webadmin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// noMembershipStore is a tenant-aware store whose user belongs to no tenant.
type noMembershipStore struct{ *saasAdminStore }

func (noMembershipStore) CanAccessTenant(context.Context, int, int) (bool, error) {
	return false, nil
}

// Regression: without X-Semidx-Tenant the request fell back to the default
// tenant with no membership check.
func TestDefaultTenantRequiresMembership(t *testing.T) {
	st := noMembershipStore{newSaaSAdminStore()}
	st.addUser("member", "supersecret", "member")
	a, err := New(st, fakeEmbedder{}, nil, true, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(a.Handler())
	t.Cleanup(srv.Close)
	c := newClient(t, srv)
	login(t, c, srv.URL, "member", "supersecret")
	if code, body := getBody(t, c, srv.URL+"/admin/api/projects"); code != http.StatusForbidden {
		t.Fatalf("non-member default tenant = %d %s, want 403", code, body)
	}

	// A global admin keeps implicit access to the default tenant.
	st.addUser("root", "supersecret", "admin")
	c2 := newClient(t, srv)
	login(t, c2, srv.URL, "root", "supersecret")
	if code, body := getBody(t, c2, srv.URL+"/admin/api/projects"); code != http.StatusOK {
		t.Fatalf("global admin default tenant = %d %s, want 200", code, body)
	}
}
