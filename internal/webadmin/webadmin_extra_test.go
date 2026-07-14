package webadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	embedpkg "github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/jwtauth"
	"github.com/lgldsilva/semidx/internal/store"
)

// fakeEmbedder is a minimal Embedder for search-page tests.
type fakeEmbedder struct{}

func (fakeEmbedder) ModelInfo(_ context.Context, m string) (*embedpkg.ModelInfo, error) {
	return &embedpkg.ModelInfo{Name: m, Dims: 3}, nil
}
func (fakeEmbedder) EmbedSingle(context.Context, string, string) ([]float32, error) {
	return []float32{1, 0, 0}, nil
}
func (fakeEmbedder) Embed(_ context.Context, _ string, inputs ...string) ([][]float32, error) {
	out := make([][]float32, len(inputs))
	for i := range inputs {
		out[i] = []float32{1, 0, 0}
	}
	return out, nil
}
func (fakeEmbedder) ListModels(context.Context) ([]string, error) {
	return []string{"bge-m3"}, nil
}

// newAdminWith builds an admin backed by a fresh store with the given embedder
// and JWT issuer, wrapped in an httptest TLS server. All tests use TLS so
// cookies with Secure=true are accepted by the test client.
func newAdminWith(t *testing.T, emb embedpkg.Embedder, jwt *jwtauth.Issuer) (*httptest.Server, *fakeStore) {
	t.Helper()
	fs := newFakeStore()
	a, err := New(fs, emb, nil, true, jwt, "")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(a.Handler())
	t.Cleanup(srv.Close)
	return srv, fs
}

// --- login form & logout -----------------------------------------------------

func TestLoginForm(t *testing.T) {
	srv, fs := newTestAdmin(t)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)

	// GET /admin/login is the React SPA shell (client-side login).
	if code, body := getBody(t, c, srv.URL+"/admin/login?err=nope"); code != 200 || !strings.Contains(body, "root") {
		t.Errorf("SPA login route = %d, missing #root", code)
	}

	// Already logged in: SPA still serves 200 (client router decides).
	login(t, c, srv.URL, "admin", "supersecret")
	resp, err := c.Get(srv.URL + "/admin/login")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("logged-in GET /admin/login = %d, want 200 SPA", resp.StatusCode)
	}
}

func TestLogout(t *testing.T) {
	srv, fs := newTestAdmin(t)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	if len(fs.sessions) != 1 {
		t.Fatalf("expected an active session, got %d", len(fs.sessions))
	}

	csrf := csrfFrom(t, c, srv.URL+"/admin/api/me")
	code, _ := postAdminJSON(t, c, srv.URL+"/admin/api/logout", csrf, map[string]any{})
	if code != http.StatusOK {
		t.Errorf("logout = %d, want 200", code)
	}
	if len(fs.sessions) != 0 {
		t.Errorf("session not deleted on logout: %d remain", len(fs.sessions))
	}
}

// --- login submit edge cases (SPA JSON API) -----------------------------------

func TestLoginSubmitEdgeCases(t *testing.T) {
	t.Run("disabled user cannot log in", func(t *testing.T) {
		srv, fs := newTestAdmin(t)
		u := fs.addUser("dave", "supersecret", "member")
		u.Disabled = true
		c := newClient(t, srv)
		code, _ := postAdminJSON(t, c, srv.URL+"/admin/api/login", "", map[string]any{
			"username": "dave", "password": "supersecret",
		})
		if code != http.StatusUnauthorized {
			t.Errorf("disabled login = %d, want 401", code)
		}
		if len(fs.sessions) != 0 {
			t.Error("disabled user got a session")
		}
	})

	t.Run("rate limit after too many attempts", func(t *testing.T) {
		srv, fs := newTestAdmin(t)
		fs.addUser("admin", "supersecret", "admin")
		c := newClient(t, srv)
		var lastCode int
		var lastBody string
		for i := 0; i < loginMaxTries+1; i++ {
			lastCode, lastBody = postAdminJSON(t, c, srv.URL+"/admin/api/login", "", map[string]any{
				"username": "admin", "password": "wrong",
			})
		}
		if lastCode != http.StatusTooManyRequests || !strings.Contains(lastBody, "too many attempts") {
			t.Errorf("expected rate-limit after %d tries; code=%d body=%q", loginMaxTries+1, lastCode, lastBody)
		}
	})

	t.Run("lookup error is a 500", func(t *testing.T) {
		srv, fs := newTestAdmin(t)
		fs.getUserErr = errors.New("db down")
		c := newClient(t, srv)
		code, _ := postAdminJSON(t, c, srv.URL+"/admin/api/login", "", map[string]any{
			"username": "x", "password": "y",
		})
		if code != http.StatusInternalServerError {
			t.Errorf("lookup error login = %d, want 500", code)
		}
	})

	t.Run("session creation error is a 500", func(t *testing.T) {
		srv, fs := newTestAdmin(t)
		fs.addUser("admin", "supersecret", "admin")
		fs.createSessErr = errors.New("insert failed")
		c := newClient(t, srv)
		code, _ := postAdminJSON(t, c, srv.URL+"/admin/api/login", "", map[string]any{
			"username": "admin", "password": "supersecret",
		})
		if code != http.StatusInternalServerError {
			t.Errorf("session-create error = %d, want 500", code)
		}
	})
}

// --- search JSON API (SPA) ----------------------------------------------------

func postSearchJSON(t *testing.T, c *http.Client, base, csrf string, body map[string]any) (int, string) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, base+"/admin/api/search", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestSearchAPI(t *testing.T) {
	t.Run("query without project shows validation error", func(t *testing.T) {
		srv, fs := newTestAdmin(t)
		fs.addUser("admin", "supersecret", "admin")
		c := newClient(t, srv)
		login(t, c, srv.URL, "admin", "supersecret")
		csrf := csrfFrom(t, c, srv.URL+"/admin/keys")
		code, body := postSearchJSON(t, c, srv.URL, csrf, map[string]any{"query": "hello"})
		if code != 400 || !strings.Contains(body, "project is required") {
			t.Errorf("missing project validation = %d, body=%q", code, body)
		}
	})

	t.Run("SPA search route serves shell", func(t *testing.T) {
		srv, fs := newTestAdmin(t)
		fs.addUser("admin", "supersecret", "admin")
		c := newClient(t, srv)
		login(t, c, srv.URL, "admin", "supersecret")
		if code, body := getBody(t, c, srv.URL+"/admin/search"); code != 200 || !strings.Contains(body, "root") {
			t.Errorf("search SPA = %d", code)
		}
	})

	t.Run("project not found surfaces an error", func(t *testing.T) {
		srv, fs := newTestAdmin(t) // searchProject nil → GetProject returns ErrNotFound
		fs.addUser("admin", "supersecret", "admin")
		c := newClient(t, srv)
		login(t, c, srv.URL, "admin", "supersecret")
		csrf := csrfFrom(t, c, srv.URL+"/admin/keys")
		code, body := postSearchJSON(t, c, srv.URL, csrf, map[string]any{"project": "ghost", "query": "hello"})
		if code != 404 || !strings.Contains(body, "project not found") {
			t.Errorf("missing-project search = %d, body=%q", code, body)
		}
	})

	t.Run("successful search returns results", func(t *testing.T) {
		srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
		fs.addUser("admin", "supersecret", "admin")
		fs.searchProject = &store.Project{ID: 1, Name: "proj", Model: "bge-m3"}
		fs.searchResults = []store.SearchResult{{FilePath: "a.go", Content: "hit", Score: 0.9}}
		c := newClient(t, srv)
		login(t, c, srv.URL, "admin", "supersecret")
		csrf := csrfFrom(t, c, srv.URL+"/admin/keys")
		code, body := postSearchJSON(t, c, srv.URL, csrf, map[string]any{"project": "proj", "query": "hello"})
		if code != 200 || !strings.Contains(body, "a.go") {
			t.Errorf("search results = %d, body=%q", code, body)
		}
	})

	t.Run("search all projects dedupes by identity", func(t *testing.T) {
		srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
		fs.addUser("admin", "supersecret", "admin")
		fs.projects = []store.Project{
			{ID: 1, Name: "jackui-a", Identity: "git:example/jackui", Model: "bge-m3"},
			{ID: 2, Name: "jackui-b", Identity: "git:example/jackui", Model: "bge-m3"},
		}
		fs.searchResults = []store.SearchResult{{FilePath: "main.go", Content: "hit", Score: 0.85}}
		c := newClient(t, srv)
		login(t, c, srv.URL, "admin", "supersecret")
		csrf := csrfFrom(t, c, srv.URL+"/admin/keys")
		code, body := postSearchJSON(t, c, srv.URL, csrf, map[string]any{"all": true, "query": "hello", "top": 5})
		if code != 200 {
			t.Fatalf("status = %d body=%s", code, body)
		}
		if !strings.Contains(body, `"project_count":1`) {
			t.Errorf("expected deduped project count, body=%q", body)
		}
	})

	t.Run("case-insensitive project name resolves", func(t *testing.T) {
		srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
		fs.addUser("admin", "supersecret", "admin")
		fs.projects = []store.Project{{ID: 1, Name: "jackui", Model: "bge-m3"}}
		fs.searchResults = []store.SearchResult{{FilePath: "a.go", Content: "hit", Score: 0.9}}
		c := newClient(t, srv)
		login(t, c, srv.URL, "admin", "supersecret")
		csrf := csrfFrom(t, c, srv.URL+"/admin/keys")
		code, body := postSearchJSON(t, c, srv.URL, csrf, map[string]any{"project": "JackUI", "query": "hello"})
		if code != 200 || !strings.Contains(body, "a.go") {
			t.Errorf("case-insensitive search = %d, body=%q", code, body)
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
		c := newClient(t, srv)
		login(t, c, srv.URL, "admin", "supersecret")
		csrf := csrfFrom(t, c, srv.URL+"/admin/keys")
		code, body := postSearchJSON(t, c, srv.URL, csrf, map[string]any{"all": true, "query": "hello", "top": 5})
		if code != 200 {
			t.Fatalf("status = %d", code)
		}
		if !strings.Contains(body, "main.go") || !strings.Contains(body, "alpha") || !strings.Contains(body, "beta") {
			t.Errorf("expected merged labeled results, body=%q", body)
		}
		if !strings.Contains(body, `"project_count":2`) {
			t.Errorf("expected project count summary, body=%q", body)
		}
	})
}

func TestSearchAllProjectsMergeError(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "alpha", Model: "bge-m3"}}
	fs.searchErr = context.Canceled
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")
	code, body := postSearchJSON(t, c, srv.URL, csrf, map[string]any{"all": true, "query": "hello"})
	// REQ-SRCH-08: infra failures return a sanitized 500 and must NOT echo the
	// raw error back to the client.
	if code != 500 || !strings.Contains(body, "search failed") {
		t.Errorf("search all merge error = %d, body=%q", code, body)
	}
	if strings.Contains(body, context.Canceled.Error()) {
		t.Errorf("raw error leaked to client: %q", body)
	}
}

func TestProjectsAPIPagination(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{
		{ID: 1, Name: "a", Model: "m1", Status: "ready"},
		{ID: 2, Name: "b", Model: "m2", Status: "ready"},
	}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	code, body := getBody(t, c, srv.URL+"/admin/api/projects?limit=1&offset=1")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	if !strings.Contains(body, `"name":"b"`) {
		t.Errorf("paginated body = %s", body)
	}
}

func TestProjectsAPIEmptyList(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = nil
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	code, body := getBody(t, c, srv.URL+"/admin/api/projects")
	if code != 200 || !strings.Contains(body, `"projects":[]`) {
		t.Errorf("empty list = %d, body=%s", code, body)
	}
}

func TestProjectsAPI(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{
		ID: 1, Name: "jackui", Model: "bge-m3", Status: "ready",
		Identity: "git:example/jackui", Path: "/data/jackui", SourceType: "git",
	}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	code, body := getBody(t, c, srv.URL+"/admin/api/projects")
	if code != 200 {
		t.Fatalf("projects api = %d", code)
	}
	if !strings.Contains(body, `"name":"jackui"`) || !strings.Contains(body, `"identity":"git:example/jackui"`) {
		t.Fatalf("unexpected api body: %s", body)
	}
}

func TestProjectsAPIListError(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.listProjectsErr = errors.New("db down")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	code, body := getBody(t, c, srv.URL+"/admin/api/projects")
	if code != 500 || !strings.Contains(body, "internal error") {
		t.Errorf("projects api error = %d, body=%q", code, body)
	}
}

func TestSearchAPISearchServiceError(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "alpha", Model: "bge-m3"}}
	fs.searchErr = errors.New("embed down")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")
	code, body := postSearchJSON(t, c, srv.URL, csrf, map[string]any{"project": "alpha", "query": "hello"})
	if code != 500 || !strings.Contains(body, "search failed") {
		t.Errorf("search service error = %d, body=%q", code, body)
	}
}

func TestSearchAllProjectsListError(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.listProjectsErr = errors.New("db down")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")
	code, body := postSearchJSON(t, c, srv.URL, csrf, map[string]any{"all": true, "query": "hello"})
	if code != 400 || !strings.Contains(body, "could not list projects") {
		t.Errorf("search all list error = %d, body=%q", code, body)
	}
}

func TestSearchAllProjectsEmpty(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")
	code, body := postSearchJSON(t, c, srv.URL, csrf, map[string]any{"all": true, "query": "hello"})
	if code != 400 || !strings.Contains(body, "no indexed projects") {
		t.Errorf("empty index = %d, body=%q", code, body)
	}
}

func TestSearchAPIShowsResolvedProject(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "jackui", Model: "bge-m3"}}
	fs.searchResults = []store.SearchResult{{FilePath: "a.go", Content: "hit", Score: 0.9}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")
	code, body := postSearchJSON(t, c, srv.URL, csrf, map[string]any{"project": "JackUI", "query": "hello", "top": 20})
	if code != 200 || !strings.Contains(body, `"resolved_project":"jackui"`) {
		t.Errorf("resolved project label = %d, body=%q", code, body)
	}
}

// --- protect: CSRF, session lookup error, stale session ---------------------

func TestProtectEdgeCases(t *testing.T) {
	t.Run("wrong CSRF token is rejected", func(t *testing.T) {
		srv, fs := newTestAdmin(t)
		fs.addUser("admin", "supersecret", "admin")
		c := newClient(t, srv)
		login(t, c, srv.URL, "admin", "supersecret")
		code, _ := postAdminJSON(t, c, srv.URL+"/admin/api/keys", "deadbeef", map[string]any{"name": "x"})
		if code != http.StatusForbidden {
			t.Errorf("wrong CSRF = %d, want 403", code)
		}
	})

	t.Run("session lookup error is a 500", func(t *testing.T) {
		srv, fs := newTestAdmin(t)
		fs.addUser("admin", "supersecret", "admin")
		c := newClient(t, srv)
		login(t, c, srv.URL, "admin", "supersecret")
		fs.sessionErr = errors.New("db down")
		if code, _ := getBody(t, c, srv.URL+"/admin/api/me"); code != http.StatusInternalServerError {
			t.Errorf("session lookup error = %d, want 500", code)
		}
	})

	t.Run("stale session returns 401 on API and clears the cookie", func(t *testing.T) {
		srv, fs := newTestAdmin(t)
		fs.addUser("admin", "supersecret", "admin")
		c := newClient(t, srv)
		login(t, c, srv.URL, "admin", "supersecret")
		fs.sessions = map[string]int{}
		resp, err := c.Get(srv.URL + "/admin/api/me")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("stale session api/me = %d, want 401", resp.StatusCode)
		}
	})
}

// --- session cookie hardening -------------------------------------------------

func loginCookie(t *testing.T, srv *httptest.Server, user, pass string) *http.Cookie {
	t.Helper()
	c := newClient(t, srv)
	raw, err := json.Marshal(map[string]any{"username": user, "password": pass})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Post(srv.URL+"/admin/api/login", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == sessionCookie {
			return ck
		}
	}
	return nil
}

func TestSessionCookieAttributes(t *testing.T) {
	t.Run("session cookie: HttpOnly, Secure, Lax, scoped to /admin", func(t *testing.T) {
		srv, fs := newTestAdmin(t) // TLS server with Secure=true cookies
		fs.addUser("admin", "supersecret", "admin")
		ck := loginCookie(t, srv, "admin", "supersecret")
		if ck == nil {
			t.Fatal("no session cookie set on login")
		}
		if !ck.HttpOnly {
			t.Error("session cookie must be HttpOnly")
		}
		if !ck.Secure {
			t.Error("session cookie must be Secure (admin is behind HTTPS)")
		}
		if ck.SameSite != http.SameSiteLaxMode {
			t.Errorf("SameSite = %v, want Lax", ck.SameSite)
		}
		if ck.Path != "/admin" {
			t.Errorf("cookie Path = %q, want /admin", ck.Path)
		}
		if ck.Value == "" {
			t.Error("session cookie has no value")
		}
	})

	t.Run("logout clears the cookie", func(t *testing.T) {
		srv, fs := newTestAdmin(t)
		fs.addUser("admin", "supersecret", "admin")
		c := newClient(t, srv)
		login(t, c, srv.URL, "admin", "supersecret")
		csrf := csrfFrom(t, c, srv.URL+"/admin/api/me")
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/api/logout", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-CSRF-Token", csrf)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("logout = %d", resp.StatusCode)
		}
		if len(fs.sessions) != 0 {
			t.Error("logout did not delete the server-side session")
		}
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

// --- projects API list error (dashboard was SPA-only; API still fails closed) --

func TestProjectsAPIListErrorLogged(t *testing.T) {
	srv, fs := newTestAdmin(t)
	fs.addUser("admin", "supersecret", "admin")
	fs.listProjectsErr = errors.New("query failed")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	// JSON projects API returns 500 when the store fails.
	if code, body := getBody(t, c, srv.URL+"/admin/api/projects"); code != 500 || !strings.Contains(body, "error") {
		t.Errorf("projects api list error = %d %s, want 500 JSON error", code, body)
	}
}
