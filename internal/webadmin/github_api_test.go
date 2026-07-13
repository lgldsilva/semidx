package webadmin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newAdminWithGitHub builds an admin server with GitHub discovery pointed at a
// fake GitHub API (baseURL). token empty leaves discovery disabled.
func newAdminWithGitHub(t *testing.T, token, baseURL string) (*httptest.Server, *fakeStore) {
	t.Helper()
	fs := newFakeStore()
	a, err := New(fs, fakeEmbedder{}, nil, true, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	a.SetGitHub(token, baseURL)
	srv := httptest.NewTLSServer(a.Handler())
	t.Cleanup(srv.Close)
	return srv, fs
}

// fakeGitHub serves a minimal /user/repos and /orgs/{org}/repos.
func fakeGitHub(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/user/repos", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer gh-pat" {
			t.Errorf("Authorization = %q", got)
		}
		_, _ = fmt.Fprint(w, `[{"full_name":"me/app","name":"app","owner":{"login":"me"},"clone_url":"https://github.com/me/app.git","private":true}]`)
	})
	mux.HandleFunc("/orgs/acme/repos", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `[{"full_name":"acme/svc","name":"svc","owner":{"login":"acme"},"clone_url":"https://github.com/acme/svc.git"}]`)
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func loginCSRF(t *testing.T, c *http.Client, srv *httptest.Server, fs *fakeStore) {
	t.Helper()
	fs.addUser("admin", "supersecret", "admin")
	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/login", "", map[string]any{
		"username": "admin", "password": "supersecret",
	})
	if code != http.StatusOK {
		t.Fatalf("login = %d body=%s", code, body)
	}
}

func TestAPIGithubRepos_User(t *testing.T) {
	gh := fakeGitHub(t)
	srv, fs := newAdminWithGitHub(t, "gh-pat", gh.URL)
	c := newClient(t, srv)
	loginCSRF(t, c, srv, fs)

	code, body := getBody(t, c, srv.URL+"/admin/api/github/repos")
	if code != http.StatusOK {
		t.Fatalf("repos = %d body=%s", code, body)
	}
	if !strings.Contains(body, `"full_name":"me/app"`) || !strings.Contains(body, `"clone_url":"https://github.com/me/app.git"`) {
		t.Errorf("unexpected repos body: %s", body)
	}
}

func TestAPIGithubRepos_Org(t *testing.T) {
	gh := fakeGitHub(t)
	srv, fs := newAdminWithGitHub(t, "gh-pat", gh.URL)
	c := newClient(t, srv)
	loginCSRF(t, c, srv, fs)

	code, body := getBody(t, c, srv.URL+"/admin/api/github/repos?org=acme")
	if code != http.StatusOK {
		t.Fatalf("org repos = %d body=%s", code, body)
	}
	if !strings.Contains(body, `"full_name":"acme/svc"`) {
		t.Errorf("unexpected org repos body: %s", body)
	}
}

func TestAPIGithubRepos_Disabled(t *testing.T) {
	srv, fs := newAdminWithGitHub(t, "", "")
	c := newClient(t, srv)
	loginCSRF(t, c, srv, fs)

	code, body := getBody(t, c, srv.URL+"/admin/api/github/repos")
	if code != http.StatusConflict {
		t.Fatalf("disabled = %d body=%s", code, body)
	}
	if !strings.Contains(body, "not configured") {
		t.Errorf("expected not-configured message, got %s", body)
	}
}

func TestAPIGithubRepos_UpstreamError(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"message":"Bad credentials"}`)
	}))
	t.Cleanup(bad.Close)
	srv, fs := newAdminWithGitHub(t, "gh-pat", bad.URL)
	c := newClient(t, srv)
	loginCSRF(t, c, srv, fs)

	code, body := getBody(t, c, srv.URL+"/admin/api/github/repos")
	if code != http.StatusBadGateway {
		t.Fatalf("upstream error = %d body=%s", code, body)
	}
	// The raw upstream error (which could reference the account) must not leak.
	if strings.Contains(body, "Bad credentials") {
		t.Errorf("upstream error leaked to client: %s", body)
	}
}

func TestAPIGithubRepos_RequiresAdmin(t *testing.T) {
	gh := fakeGitHub(t)
	srv, fs := newAdminWithGitHub(t, "gh-pat", gh.URL)
	c := newClient(t, srv)
	// A non-admin member must be forbidden (route is admin-only).
	fs.addUser("member", "supersecret", "member")
	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/login", "", map[string]any{
		"username": "member", "password": "supersecret",
	})
	if code != http.StatusOK {
		t.Fatalf("member login = %d body=%s", code, body)
	}
	code, _ = getBody(t, c, srv.URL+"/admin/api/github/repos")
	if code != http.StatusForbidden {
		t.Errorf("member GET github/repos = %d, want 403", code)
	}
}
