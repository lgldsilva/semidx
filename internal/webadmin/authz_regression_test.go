package webadmin

import (
	"net/http"
	"testing"
)

// TestUserRoutesRequireAdmin locks the admin-only boundary for user management:
// a logged-in member is refused on every /admin user route (JSON + legacy HTML),
// while an admin is allowed. This is the one role gate in the admin UI, so a
// regression here would silently let members manage accounts.
func TestUserRoutesRequireAdmin(t *testing.T) {
	adminOnly := []struct {
		method, path string
	}{
		{http.MethodGet, "/admin/api/users"},
		{http.MethodPost, "/admin/api/users"},
		{http.MethodPost, "/admin/api/users/1/disabled"},
		{http.MethodGet, "/admin/users"},
		{http.MethodPost, "/admin/users"},
		{http.MethodPost, "/admin/users/disable"},
	}

	// --- member is refused everywhere ---
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("member", "supersecret", "member")
	c := newClient(t, srv)
	login(t, c, srv.URL, "member", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	for _, rt := range adminOnly {
		code := requestStatus(t, c, rt.method, srv.URL+rt.path, csrf)
		if code != http.StatusForbidden {
			t.Errorf("member %s %s = %d, want 403", rt.method, rt.path, code)
		}
	}

	// --- admin is allowed on the read route (proves the 403s are role-based,
	// not a blanket block) ---
	srvA, fsA := newAdminWith(t, fakeEmbedder{}, nil)
	fsA.addUser("root", "supersecret", "admin")
	cA := newClient(t, srvA)
	login(t, cA, srvA.URL, "root", "supersecret")
	if code, _ := getBody(t, cA, srvA.URL+"/admin/api/users"); code != http.StatusOK {
		t.Errorf("admin GET /admin/api/users = %d, want 200", code)
	}
}

// TestMemberReachesNonAdminRoutes locks the current authorization model: routes
// other than user management are protectAPI("") — any authenticated user,
// including a member. A member can therefore list projects. If a stricter policy
// is introduced, this test must change deliberately.
func TestMemberReachesNonAdminRoutes(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("member", "supersecret", "member")
	c := newClient(t, srv)
	login(t, c, srv.URL, "member", "supersecret")

	if code, _ := getBody(t, c, srv.URL+"/admin/api/projects?limit=10"); code != http.StatusOK {
		t.Errorf("member GET /admin/api/projects = %d, want 200 (non-admin route)", code)
	}
}

// requestStatus issues method+url with the CSRF header (for mutating methods)
// and returns the status code.
func requestStatus(t *testing.T, c *http.Client, method, url, csrf string) int {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodGet {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}
