package webadmin

import (
	"net/http"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/jwtauth"
)

func TestSettingsRevokeTokenErrors(t *testing.T) {
	iss, _ := jwtauth.New("test-secret")
	srv, fs := newAdminWith(t, fakeEmbedder{}, iss)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/admin/api/tokens/not-a-number", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("invalid id = %d", resp.StatusCode)
	}

	fs.revokeErr = nil
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/admin/api/tokens/999", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("missing token = %d", resp.StatusCode)
	}
}

func TestSettingsListKeysError(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.listTokensErr = errInjected("list keys down")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, body := getBody(t, c, srv.URL+"/admin/api/keys")
	if code != 500 || !strings.Contains(body, "could not load keys") {
		t.Fatalf("list keys err = %d body=%s", code, body)
	}
}

func TestSettingsListUsersError(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.listUsersErr = errInjected("users down")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, body := getBody(t, c, srv.URL+"/admin/api/users")
	if code != 500 || !strings.Contains(body, "could not load users") {
		t.Fatalf("list users err = %d body=%s", code, body)
	}
}

type errInjected string

func (e errInjected) Error() string { return string(e) }
