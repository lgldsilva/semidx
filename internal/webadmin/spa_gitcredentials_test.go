package webadmin

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/lgldsilva/semidx/internal/secretbox"
	"github.com/lgldsilva/semidx/internal/store"
)

type gitCredAdminStore struct {
	*fakeStore
	creds map[int]*store.GitCredential
	next  int
}

func newGitCredAdminStore() *gitCredAdminStore {
	return &gitCredAdminStore{fakeStore: newFakeStore(), creds: map[int]*store.GitCredential{}}
}

func (g *gitCredAdminStore) CreateGitCredential(_ context.Context, c *store.GitCredential) (*store.GitCredential, error) {
	g.next++
	out := *c
	out.ID = g.next
	g.creds[out.ID] = &out
	return &out, nil
}
func (g *gitCredAdminStore) GetGitCredentialByID(_ context.Context, id int) (*store.GitCredential, error) {
	if c, ok := g.creds[id]; ok {
		cp := *c
		return &cp, nil
	}
	return nil, store.ErrNotFound
}
func (g *gitCredAdminStore) ListGitCredentials(_ context.Context) ([]store.GitCredential, error) {
	out := make([]store.GitCredential, 0, len(g.creds))
	for _, c := range g.creds {
		out = append(out, *c)
	}
	return out, nil
}
func (g *gitCredAdminStore) UpdateGitCredential(_ context.Context, c *store.GitCredential) error {
	if _, ok := g.creds[c.ID]; !ok {
		return store.ErrNotFound
	}
	cp := *c
	g.creds[c.ID] = &cp
	return nil
}
func (g *gitCredAdminStore) DeleteGitCredential(_ context.Context, id int) error {
	if _, ok := g.creds[id]; !ok {
		return store.ErrNotFound
	}
	delete(g.creds, id)
	return nil
}
func (g *gitCredAdminStore) GetGitCredentialForProject(context.Context, int) (*store.GitCredential, error) {
	return nil, store.ErrNotFound
}
func (g *gitCredAdminStore) GetGitCredentialForHost(context.Context, string) (*store.GitCredential, error) {
	return nil, store.ErrNotFound
}

var _ store.GitCredentialStore = (*gitCredAdminStore)(nil)

func newAdminWithGitCreds(t *testing.T) (*httptest.Server, *gitCredAdminStore, *http.Client) {
	t.Helper()
	st := newGitCredAdminStore()
	st.addUser("admin", "supersecret", "admin")
	a, err := New(st, nil, nil, true, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	box := newWebadminTestBox(t)
	a.SetSecretBox(box)
	srv := httptest.NewTLSServer(a.Handler())
	t.Cleanup(srv.Close)
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	return srv, st, c
}

func newWebadminTestBox(t *testing.T) *secretbox.Box {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	box, err := secretbox.New(hex.EncodeToString(raw))
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func genAdminSSHPEM(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pemBlock, err := ssh.MarshalPrivateKey(priv, "test")
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(pemBlock))
}

func TestGitCredentialsSPA_CRUD(t *testing.T) {
	srv, st, c := newAdminWithGitCreds(t)
	csrf := csrfFrom(t, c, srv.URL+"/admin/api/me")
	pem := genAdminSSHPEM(t)

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/git-credentials", csrf, map[string]any{
		"host": "gitea.lan", "kind": "ssh", "username": "git", "secret": pem, "label": "deploy",
	})
	if code != http.StatusCreated {
		t.Fatalf("create = %d %s", code, body)
	}
	if strings.Contains(body, "BEGIN OPENSSH") {
		t.Fatalf("create leaks pem: %s", body)
	}
	if len(st.creds) != 1 {
		t.Fatalf("stored=%d", len(st.creds))
	}

	code, body = getBody(t, c, srv.URL+"/admin/api/git-credentials")
	if code != 200 || !strings.Contains(body, "gitea.lan") {
		t.Fatalf("list = %d %s", code, body)
	}

	code, body = putAdminJSON(t, c, srv.URL+"/admin/api/git-credentials/1", csrf, map[string]any{
		"kind": "ssh", "username": "git", "label": "rotated",
	})
	if code != 200 || !strings.Contains(body, "rotated") {
		t.Fatalf("update = %d %s", code, body)
	}

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/admin/api/git-credentials/1", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
}

func TestGitCredentialsSPA_Errors(t *testing.T) {
	srv, _, c := newAdminWithGitCreds(t)
	csrf := csrfFrom(t, c, srv.URL+"/admin/api/me")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/git-credentials", csrf, map[string]any{
		"host": "h", "project_id": 1, "kind": "https", "secret": "x",
	})
	if code != 400 || !strings.Contains(body, "exactly one") {
		t.Fatalf("both scopes = %d %s", code, body)
	}

	code, body = putAdminJSON(t, c, srv.URL+"/admin/api/git-credentials/x", csrf, map[string]any{"kind": "https"})
	if code != 400 {
		t.Fatalf("bad id = %d %s", code, body)
	}

	code, body = putAdminJSON(t, c, srv.URL+"/admin/api/git-credentials/99", csrf, map[string]any{"kind": "https"})
	if code != 404 {
		t.Fatalf("missing = %d %s", code, body)
	}

	// No secretbox → 503
	st := newGitCredAdminStore()
	st.addUser("admin", "supersecret", "admin")
	a, err := New(st, nil, nil, true, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	srv2 := httptest.NewTLSServer(a.Handler())
	t.Cleanup(srv2.Close)
	c2 := newClient(t, srv2)
	login(t, c2, srv2.URL, "admin", "supersecret")
	csrf2 := csrfFrom(t, c2, srv2.URL+"/admin/api/me")
	code, body = postAdminJSON(t, c2, srv2.URL+"/admin/api/git-credentials", csrf2, map[string]any{
		"host": "h.com", "kind": "https", "secret": "tok",
	})
	if code != 503 {
		t.Fatalf("no box = %d %s", code, body)
	}

	// Unsupported store
	fs := newFakeStore()
	fs.addUser("admin", "supersecret", "admin")
	a2, err := New(fs, nil, nil, true, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	a2.SetSecretBox(newWebadminTestBox(t))
	srv3 := httptest.NewTLSServer(a2.Handler())
	t.Cleanup(srv3.Close)
	c3 := newClient(t, srv3)
	login(t, c3, srv3.URL, "admin", "supersecret")
	code, body = getBody(t, c3, srv3.URL+"/admin/api/git-credentials")
	if code != 501 {
		t.Fatalf("unsupported = %d %s", code, body)
	}
}

func putAdminJSON(t *testing.T, c *http.Client, url, csrf string, body any) (int, string) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}
