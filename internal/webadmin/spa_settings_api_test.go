package webadmin

import (
	"net/http"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/jwtauth"
	"github.com/lgldsilva/semidx/internal/store"
)

func TestSettingsKeysAPI(t *testing.T) {
	iss, _ := jwtauth.New("test-secret")
	srv, fs := newAdminWith(t, fakeEmbedder{}, iss)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := getBody(t, c, srv.URL+"/admin/api/keys")
	if code != 200 || !strings.Contains(body, `"keys"`) {
		t.Fatalf("list keys = %d body=%s", code, body)
	}

	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/keys", csrf, map[string]any{
		"name": "ci-key", "scopes": []string{"read"},
	})
	if code != http.StatusCreated || !strings.Contains(body, `"token"`) {
		t.Fatalf("create key = %d body=%s", code, body)
	}

	code, body = getBody(t, c, srv.URL+"/admin/api/keys")
	if code != 200 || !strings.Contains(body, `"ci-key"`) {
		t.Fatalf("list after create = %d body=%s", code, body)
	}

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/admin/api/keys/1", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("revoke key = %d", resp.StatusCode)
	}
}

func TestSettingsTokensAndPasswordAPI(t *testing.T) {
	iss, _ := jwtauth.New("test-secret")
	srv, fs := newAdminWith(t, fakeEmbedder{}, iss)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := getBody(t, c, srv.URL+"/admin/api/tokens")
	if code != 200 || !strings.Contains(body, `"enabled":true`) {
		t.Fatalf("list tokens = %d body=%s", code, body)
	}

	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/tokens", csrf, map[string]any{
		"name": "ctl", "scopes": []string{"read"}, "ttl_days": 7,
	})
	if code != http.StatusCreated || !strings.Contains(body, `"token"`) {
		t.Fatalf("create token = %d body=%s", code, body)
	}

	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/account/password", csrf, map[string]any{
		"current": "supersecret", "new": "newpassword1",
	})
	if code != 200 || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("change password = %d body=%s", code, body)
	}
}

func TestSettingsUsersAPI(t *testing.T) {
	iss, _ := jwtauth.New("test-secret")
	srv, fs := newAdminWith(t, fakeEmbedder{}, iss)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := getBody(t, c, srv.URL+"/admin/api/users")
	if code != 200 || !strings.Contains(body, `"admin"`) {
		t.Fatalf("list users = %d body=%s", code, body)
	}

	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/users", csrf, map[string]any{
		"username": "bob", "password": "bobsecret1", "role": "member",
	})
	if code != http.StatusCreated || !strings.Contains(body, `"bob"`) {
		t.Fatalf("create user = %d body=%s", code, body)
	}

	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/users/2/disabled", csrf, map[string]any{"disabled": true})
	if code != 200 {
		t.Fatalf("disable user = %d body=%s", code, body)
	}
}

func TestProjectDetailAPI(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3", Status: "ready", Dims: 3}}
	fs.fileHashes = map[string]string{"a.go": "h1", "b.go": "h2"}
	fs.fileCount = 2
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, body := getBody(t, c, srv.URL+"/admin/api/projects/demo")
	if code != 200 || !strings.Contains(body, `"name":"demo"`) || !strings.Contains(body, `"ext_breakdown"`) {
		t.Fatalf("detail = %d body=%s", code, body)
	}
}

func TestParseJSONScopes(t *testing.T) {
	got, err := parseJSONScopes([]string{"read", "write"}, "admin")
	if err != nil || len(got) != 2 {
		t.Fatalf("scopes=%v err=%v", got, err)
	}
	if _, err := parseJSONScopes([]string{"admin"}, "member"); err == nil {
		t.Fatal("member cannot request admin scope")
	}
}

func TestSettingsTokensDisabled(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := getBody(t, c, srv.URL+"/admin/api/tokens")
	if code != 200 || !strings.Contains(body, `"enabled":false`) {
		t.Fatalf("tokens disabled = %d body=%s", code, body)
	}

	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/tokens", csrf, map[string]any{
		"name": "x", "scopes": []string{"read"},
	})
	if code != 403 {
		t.Fatalf("create token disabled = %d body=%s", code, body)
	}
}

func TestSettingsRevokeKeyNotFound(t *testing.T) {
	iss, _ := jwtauth.New("test-secret")
	srv, fs := newAdminWith(t, fakeEmbedder{}, iss)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/admin/api/keys/999", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("revoke missing key = %d", resp.StatusCode)
	}
}
