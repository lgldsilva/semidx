package webadmin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	embedpkg "github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/jwtauth"
	"github.com/lgldsilva/semidx/internal/store"
)

// fakeEmbedder is a minimal Embedder for search-page tests.
type fakeEmbedder struct{ embedpkg.Embedder }

func (fakeEmbedder) ModelInfo(_ context.Context, m string) (*embedpkg.ModelInfo, error) {
	return &embedpkg.ModelInfo{Name: m, Dims: 3}, nil
}
func (fakeEmbedder) EmbedSingle(context.Context, string, string) ([]float32, error) {
	return []float32{1, 0, 0}, nil
}

// newAdminWith builds an admin backed by a fresh store with the given embedder
// and JWT issuer, wrapped in an httptest server.
func newAdminWith(t *testing.T, emb embedpkg.Embedder, jwt *jwtauth.Issuer) (*httptest.Server, *fakeStore) {
	t.Helper()
	fs := newFakeStore()
	a, err := New(fs, emb, nil, false, jwt, "")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	return srv, fs
}

// post issues a form POST and returns the status code and body.
func post(t *testing.T, c *http.Client, urlStr string, form url.Values) (int, string) {
	t.Helper()
	resp, err := c.PostForm(urlStr, form)
	if err != nil {
		t.Fatal(err)
	}
	return readAll(resp)
}

// --- login form & logout -----------------------------------------------------

func TestLoginForm(t *testing.T) {
	srv, fs := newTestAdmin(t)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t)

	// Not logged in: the form renders with 200, and an ?err= flashes.
	if code, body := getBody(t, c, srv.URL+"/admin/login?err=nope"); code != 200 || !strings.Contains(body, `name="password"`) {
		t.Errorf("login form = %d, missing form fields", code)
	}

	// Already logged in: GET /admin/login redirects to the dashboard.
	login(t, c, srv.URL, "admin", "supersecret")
	resp, err := c.Get(srv.URL + "/admin/login")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("logged-in GET /admin/login = %d, want 303", resp.StatusCode)
	}
}

func TestLogout(t *testing.T) {
	srv, fs := newTestAdmin(t)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t)
	login(t, c, srv.URL, "admin", "supersecret")
	if len(fs.sessions) != 1 {
		t.Fatalf("expected an active session, got %d", len(fs.sessions))
	}

	csrf := csrfFrom(t, c, srv.URL+"/admin/")
	code, _ := post(t, c, srv.URL+"/admin/logout", url.Values{"csrf_token": {csrf}})
	if code != http.StatusSeeOther {
		t.Errorf("logout = %d, want 303", code)
	}
	if len(fs.sessions) != 0 {
		t.Errorf("session not deleted on logout: %d remain", len(fs.sessions))
	}
}

// --- login submit edge cases -------------------------------------------------

func TestLoginSubmitEdgeCases(t *testing.T) {
	t.Run("disabled user cannot log in", func(t *testing.T) {
		srv, fs := newTestAdmin(t)
		u := fs.addUser("dave", "supersecret", "member")
		u.Disabled = true
		c := newClient(t)
		code, _ := post(t, c, srv.URL+"/admin/login", url.Values{"username": {"dave"}, "password": {"supersecret"}})
		if code != http.StatusOK { // re-renders the form, no session
			t.Errorf("disabled login = %d, want 200", code)
		}
		if len(fs.sessions) != 0 {
			t.Error("disabled user got a session")
		}
	})

	t.Run("rate limit after too many attempts", func(t *testing.T) {
		srv, fs := newTestAdmin(t)
		fs.addUser("admin", "supersecret", "admin")
		c := newClient(t)
		var lastBody string
		for i := 0; i < loginMaxTries+1; i++ {
			_, lastBody = post(t, c, srv.URL+"/admin/login", url.Values{"username": {"admin"}, "password": {"wrong"}})
		}
		if !strings.Contains(lastBody, "too many attempts") {
			t.Errorf("expected rate-limit message after %d tries; body=%q", loginMaxTries+1, lastBody)
		}
	})

	t.Run("lookup error is a 500", func(t *testing.T) {
		srv, fs := newTestAdmin(t)
		fs.getUserErr = errors.New("db down")
		c := newClient(t)
		code, _ := post(t, c, srv.URL+"/admin/login", url.Values{"username": {"x"}, "password": {"y"}})
		if code != http.StatusInternalServerError {
			t.Errorf("lookup error login = %d, want 500", code)
		}
	})

	t.Run("session creation error is a 500", func(t *testing.T) {
		srv, fs := newTestAdmin(t)
		fs.addUser("admin", "supersecret", "admin")
		fs.createSessErr = errors.New("insert failed")
		c := newClient(t)
		code, _ := post(t, c, srv.URL+"/admin/login", url.Values{"username": {"admin"}, "password": {"supersecret"}})
		if code != http.StatusInternalServerError {
			t.Errorf("session-create error = %d, want 500", code)
		}
	})
}

// --- search page --------------------------------------------------------------

func TestSearchPage(t *testing.T) {
	t.Run("no query renders the empty form", func(t *testing.T) {
		srv, fs := newTestAdmin(t)
		fs.addUser("admin", "supersecret", "admin")
		c := newClient(t)
		login(t, c, srv.URL, "admin", "supersecret")
		if code, _ := getBody(t, c, srv.URL+"/admin/search"); code != 200 {
			t.Errorf("search page = %d, want 200", code)
		}
	})

	t.Run("project not found surfaces an error", func(t *testing.T) {
		srv, fs := newTestAdmin(t) // searchProject nil → GetProject returns ErrNotFound
		fs.addUser("admin", "supersecret", "admin")
		c := newClient(t)
		login(t, c, srv.URL, "admin", "supersecret")
		code, body := getBody(t, c, srv.URL+"/admin/search?project=ghost&q=hello")
		if code != 200 || !strings.Contains(body, "project not found") {
			t.Errorf("missing-project search = %d, body=%q", code, body)
		}
	})

	t.Run("successful search renders results", func(t *testing.T) {
		srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
		fs.addUser("admin", "supersecret", "admin")
		fs.searchProject = &store.Project{ID: 1, Name: "proj", Model: "bge-m3"}
		fs.searchResults = []store.SearchResult{{FilePath: "a.go", Content: "hit", Score: 0.9}}
		c := newClient(t)
		login(t, c, srv.URL, "admin", "supersecret")
		code, body := getBody(t, c, srv.URL+"/admin/search?project=proj&q=hello")
		if code != 200 || !strings.Contains(body, "a.go") {
			t.Errorf("search results = %d, body=%q", code, body)
		}
	})

	t.Run("search all projects merges and labels results", func(t *testing.T) {
		srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
		fs.addUser("admin", "supersecret", "admin")
		fs.projects = []store.Project{
			{ID: 1, Name: "alpha", Model: "bge-m3"},
			{ID: 2, Name: "beta", Model: "bge-m3"},
		}
		fs.searchResults = []store.SearchResult{{FilePath: "main.go", Content: "hit", Score: 0.85}}
		c := newClient(t)
		login(t, c, srv.URL, "admin", "supersecret")
		code, body := getBody(t, c, srv.URL+"/admin/search?all=1&q=hello&top=5")
		if code != 200 {
			t.Fatalf("status = %d", code)
		}
		if !strings.Contains(body, "main.go") || !strings.Contains(body, "alpha") || !strings.Contains(body, "beta") {
			t.Errorf("expected merged labeled results, body=%q", body)
		}
		if !strings.Contains(body, "Searched 2 project") {
			t.Errorf("expected project count summary, body=%q", body)
		}
	})
}

// --- account (password change) ------------------------------------------------

func TestAccount(t *testing.T) {
	srv, fs := newTestAdmin(t)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t)
	login(t, c, srv.URL, "admin", "supersecret")

	if code, _ := getBody(t, c, srv.URL+"/admin/account"); code != 200 {
		t.Fatalf("account form = %d, want 200", code)
	}

	csrf := csrfFrom(t, c, srv.URL+"/admin/account")

	// Wrong current password.
	if _, body := post(t, c, srv.URL+"/admin/account", url.Values{
		"csrf_token": {csrf}, "current": {"nope"}, "new": {"newpassword"},
	}); !strings.Contains(body, "current password is incorrect") {
		t.Errorf("wrong current password not reported; body=%q", body)
	}

	// New password too short.
	if _, body := post(t, c, srv.URL+"/admin/account", url.Values{
		"csrf_token": {csrf}, "current": {"supersecret"}, "new": {"short"},
	}); !strings.Contains(body, "at least 8 characters") {
		t.Errorf("short password not reported; body=%q", body)
	}

	// Successful change.
	if _, body := post(t, c, srv.URL+"/admin/account", url.Values{
		"csrf_token": {csrf}, "current": {"supersecret"}, "new": {"newpassword"},
	}); !strings.Contains(body, "Password updated") {
		t.Errorf("password change not confirmed; body=%q", body)
	}
}

func TestAccountStoreError(t *testing.T) {
	srv, fs := newTestAdmin(t)
	fs.addUser("admin", "supersecret", "admin")
	fs.setPwErr = errors.New("write failed")
	c := newClient(t)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/account")
	if _, body := post(t, c, srv.URL+"/admin/account", url.Values{
		"csrf_token": {csrf}, "current": {"supersecret"}, "new": {"newpassword"},
	}); !strings.Contains(body, "could not update password") {
		t.Errorf("store error not reported; body=%q", body)
	}
}

// --- keys error paths ---------------------------------------------------------

func TestKeysErrorPaths(t *testing.T) {
	srv, fs := newTestAdmin(t)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t)
	login(t, c, srv.URL, "admin", "supersecret")

	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	// Empty name.
	if _, body := post(t, c, srv.URL+"/admin/keys", url.Values{"csrf_token": {csrf}, "name": {""}}); !strings.Contains(body, "a key name is required") {
		t.Errorf("empty key name not reported; body=%q", body)
	}

	// Revoke with a non-numeric id.
	if _, body := post(t, c, srv.URL+"/admin/keys/revoke", url.Values{"csrf_token": {csrf}, "id": {"abc"}}); !strings.Contains(body, "invalid key id") {
		t.Errorf("invalid key id not reported; body=%q", body)
	}

	// Revoke a key that isn't owned → not found.
	if _, body := post(t, c, srv.URL+"/admin/keys/revoke", url.Values{"csrf_token": {csrf}, "id": {"999"}}); !strings.Contains(body, "key not found") {
		t.Errorf("missing key not reported; body=%q", body)
	}

	// Store error on create.
	fs.createTokErr = errors.New("insert failed")
	if _, body := post(t, c, srv.URL+"/admin/keys", url.Values{"csrf_token": {csrf}, "name": {"laptop"}}); !strings.Contains(body, "could not create key") {
		t.Errorf("create error not reported; body=%q", body)
	}
	fs.createTokErr = nil

	// Store error on revoke.
	fs.revokeErr = errors.New("delete failed")
	if _, body := post(t, c, srv.URL+"/admin/keys/revoke", url.Values{"csrf_token": {csrf}, "id": {"1"}}); !strings.Contains(body, "could not revoke key") {
		t.Errorf("revoke error not reported; body=%q", body)
	}
	fs.revokeErr = nil

	// List error surfaces on the keys page.
	fs.listTokensErr = errors.New("query failed")
	if _, body := getBody(t, c, srv.URL+"/admin/keys"); !strings.Contains(body, "could not load keys") {
		t.Errorf("list error not reported; body=%q", body)
	}
}

// --- users error paths --------------------------------------------------------

func TestUsersErrorPaths(t *testing.T) {
	srv, fs := newTestAdmin(t)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t)
	login(t, c, srv.URL, "admin", "supersecret")

	csrf := csrfFrom(t, c, srv.URL+"/admin/users")

	// Validation: short password.
	if _, body := post(t, c, srv.URL+"/admin/users", url.Values{
		"csrf_token": {csrf}, "username": {"eve"}, "password": {"short"}, "role": {"member"},
	}); !strings.Contains(body, "at least 8 characters") {
		t.Errorf("short password not reported; body=%q", body)
	}

	// Duplicate username.
	fs.addUser("frank", "supersecret", "member")
	if _, body := post(t, c, srv.URL+"/admin/users", url.Values{
		"csrf_token": {csrf}, "username": {"frank"}, "password": {"supersecret"}, "role": {"member"},
	}); !strings.Contains(body, "already exists") {
		t.Errorf("duplicate user not reported; body=%q", body)
	}

	// Generic create error.
	fs.createUserErr = errors.New("insert failed")
	if _, body := post(t, c, srv.URL+"/admin/users", url.Values{
		"csrf_token": {csrf}, "username": {"grace"}, "password": {"supersecret"}, "role": {"admin"},
	}); !strings.Contains(body, "could not create user") {
		t.Errorf("create error not reported; body=%q", body)
	}
	fs.createUserErr = nil

	// Disable: invalid id.
	if _, body := post(t, c, srv.URL+"/admin/users/disable", url.Values{"csrf_token": {csrf}, "id": {"abc"}}); !strings.Contains(body, "invalid user id") {
		t.Errorf("invalid user id not reported; body=%q", body)
	}

	// Disable: unknown id → not found.
	if _, body := post(t, c, srv.URL+"/admin/users/disable", url.Values{"csrf_token": {csrf}, "id": {"9999"}, "disabled": {"true"}}); !strings.Contains(body, "user not found") {
		t.Errorf("unknown user disable not reported; body=%q", body)
	}

	// Disable: success on another user.
	other := fs.addUser("heidi", "supersecret", "member")
	if _, body := post(t, c, srv.URL+"/admin/users/disable", url.Values{
		"csrf_token": {csrf}, "id": {strconv.Itoa(other.ID)}, "disabled": {"true"},
	}); !strings.Contains(body, "User updated") {
		t.Errorf("disable success not reported; body=%q", body)
	}
	if !fs.users[other.ID].Disabled {
		t.Error("target user was not disabled")
	}

	// Disable: generic store error.
	fs.setDisabledErr = errors.New("update failed")
	if _, body := post(t, c, srv.URL+"/admin/users/disable", url.Values{
		"csrf_token": {csrf}, "id": {strconv.Itoa(other.ID)}, "disabled": {"false"},
	}); !strings.Contains(body, "could not update user") {
		t.Errorf("disable store error not reported; body=%q", body)
	}
	fs.setDisabledErr = nil

	// List error surfaces on the users page.
	fs.listUsersErr = errors.New("query failed")
	if _, body := getBody(t, c, srv.URL+"/admin/users"); !strings.Contains(body, "could not load users") {
		t.Errorf("list error not reported; body=%q", body)
	}
}

// --- control-token error paths ------------------------------------------------

func TestTokensErrorPaths(t *testing.T) {
	srv, fs := newTestAdmin(t)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t)
	login(t, c, srv.URL, "admin", "supersecret")

	csrf := csrfFrom(t, c, srv.URL+"/admin/tokens")

	// Empty name.
	if _, body := post(t, c, srv.URL+"/admin/tokens", url.Values{"csrf_token": {csrf}, "name": {""}}); !strings.Contains(body, "a token name is required") {
		t.Errorf("empty token name not reported; body=%q", body)
	}

	// Success with an explicit TTL (exercises parseTTLDays and admin scope).
	if _, body := post(t, c, srv.URL+"/admin/tokens", url.Values{
		"csrf_token": {csrf}, "name": {"ci"}, "scopes": {"read", "admin"}, "ttl_days": {"30"},
	}); !strings.Contains(body, "Control token created") {
		t.Errorf("token create with ttl not confirmed; body=%q", body)
	}

	// Store error on create.
	fs.createJWTErr = errors.New("insert failed")
	if _, body := post(t, c, srv.URL+"/admin/tokens", url.Values{"csrf_token": {csrf}, "name": {"boom"}}); !strings.Contains(body, "could not create token") {
		t.Errorf("token create error not reported; body=%q", body)
	}
	fs.createJWTErr = nil

	// Revoke: invalid id.
	if _, body := post(t, c, srv.URL+"/admin/tokens/revoke", url.Values{"csrf_token": {csrf}, "id": {"abc"}}); !strings.Contains(body, "invalid token id") {
		t.Errorf("invalid token id not reported; body=%q", body)
	}

	// Revoke: unowned id → not found.
	if _, body := post(t, c, srv.URL+"/admin/tokens/revoke", url.Values{"csrf_token": {csrf}, "id": {"9999"}}); !strings.Contains(body, "token not found") {
		t.Errorf("unknown token revoke not reported; body=%q", body)
	}

	// Revoke: generic store error.
	fs.revokeErr = errors.New("delete failed")
	if _, body := post(t, c, srv.URL+"/admin/tokens/revoke", url.Values{"csrf_token": {csrf}, "id": {"1"}}); !strings.Contains(body, "could not revoke token") {
		t.Errorf("token revoke error not reported; body=%q", body)
	}
	fs.revokeErr = nil

	// List error surfaces on the tokens page.
	fs.listTokensErr = errors.New("query failed")
	if _, body := getBody(t, c, srv.URL+"/admin/tokens"); !strings.Contains(body, "could not load control tokens") {
		t.Errorf("token list error not reported; body=%q", body)
	}
}

func TestTokensNonAdminAdminScope(t *testing.T) {
	srv, fs := newTestAdmin(t)
	fs.addUser("bob", "supersecret", "member")
	c := newClient(t)
	login(t, c, srv.URL, "bob", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/tokens")
	if _, body := post(t, c, srv.URL+"/admin/tokens", url.Values{
		"csrf_token": {csrf}, "name": {"x"}, "scopes": {"admin"},
	}); !strings.Contains(body, "only admins can issue admin-scoped tokens") {
		t.Errorf("member issuing admin token not blocked; body=%q", body)
	}
}

func TestKeysNonAdminAdminScope(t *testing.T) {
	srv, fs := newTestAdmin(t)
	fs.addUser("bob", "supersecret", "member")
	c := newClient(t)
	login(t, c, srv.URL, "bob", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")
	if _, body := post(t, c, srv.URL+"/admin/keys", url.Values{
		"csrf_token": {csrf}, "name": {"laptop"}, "scopes": {"admin"},
	}); !strings.Contains(body, "only admins can issue admin-scoped tokens") {
		t.Errorf("member issuing admin API key not blocked; body=%q", body)
	}
}

func TestKeysInvalidScope(t *testing.T) {
	srv, fs := newTestAdmin(t)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")
	if _, body := post(t, c, srv.URL+"/admin/keys", url.Values{
		"csrf_token": {csrf}, "name": {"x"}, "scopes": {"superuser"},
	}); !strings.Contains(body, "invalid scope") {
		t.Errorf("invalid scope not reported; body=%q", body)
	}
}

func TestTokensDisabled(t *testing.T) {
	// jwt nil → control tokens feature is off.
	srv, fs := newAdminWith(t, nil, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/tokens")
	if code, _ := post(t, c, srv.URL+"/admin/tokens", url.Values{"csrf_token": {csrf}, "name": {"x"}}); code != http.StatusForbidden {
		t.Errorf("tokensCreate with JWT disabled = %d, want 403", code)
	}
}

// --- protect: CSRF, session lookup error, expired session ---------------------

func TestProtectEdgeCases(t *testing.T) {
	t.Run("wrong CSRF token is rejected", func(t *testing.T) {
		srv, fs := newTestAdmin(t)
		fs.addUser("admin", "supersecret", "admin")
		c := newClient(t)
		login(t, c, srv.URL, "admin", "supersecret")
		if code, _ := post(t, c, srv.URL+"/admin/keys", url.Values{"csrf_token": {"deadbeef"}, "name": {"x"}}); code != http.StatusForbidden {
			t.Errorf("wrong CSRF = %d, want 403", code)
		}
	})

	t.Run("session lookup error is a 500", func(t *testing.T) {
		srv, fs := newTestAdmin(t)
		fs.addUser("admin", "supersecret", "admin")
		c := newClient(t)
		login(t, c, srv.URL, "admin", "supersecret")
		fs.sessionErr = errors.New("db down")
		if code, _ := getBody(t, c, srv.URL+"/admin/"); code != http.StatusInternalServerError {
			t.Errorf("session lookup error = %d, want 500", code)
		}
	})

	t.Run("stale session redirects to login and clears the cookie", func(t *testing.T) {
		srv, fs := newTestAdmin(t)
		fs.addUser("admin", "supersecret", "admin")
		c := newClient(t)
		login(t, c, srv.URL, "admin", "supersecret")
		// Drop the session server-side; the cookie is now stale.
		fs.sessions = map[string]int{}
		resp, err := c.Get(srv.URL + "/admin/")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("stale session dashboard = %d, want 303", resp.StatusCode)
		}
	})
}

// --- session cookie hardening -------------------------------------------------

// loginRaw performs a login and returns the *http.Cookie the server set (or nil).
func loginCookie(t *testing.T, base string, user, pass string) *http.Cookie {
	t.Helper()
	c := newClient(t)
	resp, err := c.PostForm(base+"/admin/login", url.Values{"username": {user}, "password": {pass}})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	for _, ck := range resp.Cookies() {
		if ck.Name == sessionCookie {
			return ck
		}
	}
	return nil
}

func TestSessionCookieAttributes(t *testing.T) {
	t.Run("insecure server: HttpOnly, Lax, not Secure, scoped to /admin", func(t *testing.T) {
		srv, fs := newTestAdmin(t) // secure=false
		fs.addUser("admin", "supersecret", "admin")
		ck := loginCookie(t, srv.URL, "admin", "supersecret")
		if ck == nil {
			t.Fatal("no session cookie set on login")
		}
		if !ck.HttpOnly {
			t.Error("session cookie must be HttpOnly")
		}
		if ck.SameSite != http.SameSiteLaxMode {
			t.Errorf("SameSite = %v, want Lax", ck.SameSite)
		}
		if ck.Secure {
			t.Error("insecure server must NOT set Secure")
		}
		if ck.Path != "/admin" {
			t.Errorf("cookie Path = %q, want /admin", ck.Path)
		}
		if ck.Value == "" {
			t.Error("session cookie has no value")
		}
	})

	t.Run("secure server sets the Secure flag", func(t *testing.T) {
		fs := newFakeStore()
		fs.addUser("admin", "supersecret", "admin")
		a, err := New(fs, nil, nil, true /* secureCookies */, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(a.Handler())
		t.Cleanup(srv.Close)
		ck := loginCookie(t, srv.URL, "admin", "supersecret")
		if ck == nil {
			t.Fatal("no session cookie set on login")
		}
		if !ck.Secure {
			t.Error("secure server must set Secure on the session cookie")
		}
	})

	t.Run("logout clears the cookie", func(t *testing.T) {
		srv, fs := newTestAdmin(t)
		fs.addUser("admin", "supersecret", "admin")
		c := newClient(t)
		login(t, c, srv.URL, "admin", "supersecret")
		csrf := csrfFrom(t, c, srv.URL+"/admin/")
		resp, err := c.PostForm(srv.URL+"/admin/logout", url.Values{"csrf_token": {csrf}})
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		var cleared bool
		for _, ck := range resp.Cookies() {
			if ck.Name == sessionCookie && ck.MaxAge < 0 {
				cleared = true
			}
		}
		if !cleared {
			t.Error("logout did not emit a cookie with MaxAge<0 to clear the session")
		}
	})
}

// --- dashboard list error (also exercises the nil-logger discard sink) --------

func TestDashboardListError(t *testing.T) {
	srv, fs := newTestAdmin(t)
	fs.addUser("admin", "supersecret", "admin")
	fs.listProjectsErr = errors.New("query failed")
	c := newClient(t)
	login(t, c, srv.URL, "admin", "supersecret")
	// The dashboard still renders (the error is logged, not fatal).
	if code, _ := getBody(t, c, srv.URL+"/admin/"); code != 200 {
		t.Errorf("dashboard with list error = %d, want 200", code)
	}
}
