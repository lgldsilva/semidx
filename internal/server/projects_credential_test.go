package server

import (
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func TestCreateProjectInlineCredentialRequiresAdmin(t *testing.T) {
	t.Parallel()
	st := newGitCredAPIStore(&store.Token{Scopes: []string{"write"}})
	box := newTestBox(t)
	srv := New(st, fakeEmbedder{}, nil)
	srv.SetSecretBox(box)

	pem, _, _ := genTestSSHPEM(t)
	body := `{
		"name":"priv",
		"source":{"type":"git","url":"git@gitea.lan:o/r.git","branch":"main"},
		"credential":{"kind":"ssh","username":"git","secret":` + mustJSONString(pem) + `}
	}`
	rec := do(t, srv, "POST", "/api/v1/projects", "tok", body)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "admin scope") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateProjectInlineCredentialHappyPath(t *testing.T) {
	t.Parallel()
	st := newGitCredAPIStore(&store.Token{Scopes: []string{"write", "admin"}})
	box := newTestBox(t)
	srv := New(st, fakeEmbedder{}, nil)
	srv.SetSecretBox(box)

	pem, _, wantFP := genTestSSHPEM(t)
	body := `{
		"name":"priv",
		"source":{"type":"git","url":"git@gitea.lan:o/r.git","branch":"main"},
		"credential":{"kind":"ssh","username":"git","secret":` + mustJSONString(pem) + `,"label":"deploy"}
	}`
	rec := do(t, srv, "POST", "/api/v1/projects", "tok", body)
	if rec.Code != 201 {
		t.Fatalf("create project status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(st.creds) != 1 {
		t.Fatalf("want 1 stored credential, got %d", len(st.creds))
	}
	for _, c := range st.creds {
		if c.ProjectID == nil {
			t.Fatalf("credential missing projectID: %+v", c)
		}
	}
	// Fingerprint available via list API.
	rec = do(t, srv, "GET", "/api/v1/git-credentials", "tok", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), wantFP) {
		t.Fatalf("list after inline create: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCreateProjectCredentialRejectedOnPush(t *testing.T) {
	t.Parallel()
	st := newGitCredAPIStore(&store.Token{Scopes: []string{"write", "admin"}})
	srv := New(st, fakeEmbedder{}, nil)
	srv.SetSecretBox(newTestBox(t))

	body := `{
		"name":"docs",
		"source":{"type":"push"},
		"credential":{"kind":"https","secret":"tok"}
	}`
	rec := do(t, srv, "POST", "/api/v1/projects", "tok", body)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "only supported for git") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateProjectValidationErrors(t *testing.T) {
	t.Parallel()
	srv := New(&fakeStore{token: &store.Token{Scopes: []string{"write"}}}, fakeEmbedder{}, nil)

	cases := []struct {
		body string
		want int
		sub  string
	}{
		{`{`, 400, "invalid JSON"},
		{`{"name":"","source":{"type":"push"}}`, 400, "required"},
		{`{"name":"ok","source":{"type":"svn"}}`, 400, "push' or 'git"},
		{`{"name":"ok","source":{"type":"git"}}`, 400, "source.url"},
	}
	for _, tc := range cases {
		rec := do(t, srv, "POST", "/api/v1/projects", "tok", tc.body)
		if rec.Code != tc.want || !strings.Contains(rec.Body.String(), tc.sub) {
			t.Fatalf("body=%q → %d %s (want %d containing %q)", tc.body, rec.Code, rec.Body.String(), tc.want, tc.sub)
		}
	}
}

func TestCreateProjectConflict(t *testing.T) {
	t.Parallel()
	fs := &fakeStore{
		token:     &store.Token{Scopes: []string{"write"}},
		createErr: store.ErrProjectExists,
	}
	srv := New(fs, fakeEmbedder{}, nil)
	rec := do(t, srv, "POST", "/api/v1/projects", "tok", `{"name":"dup","source":{"type":"push"}}`)
	if rec.Code != 409 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetDeleteProjectNotFound(t *testing.T) {
	t.Parallel()
	fs := &fakeStore{
		token:     &store.Token{Scopes: []string{"write", "read"}},
		deleteErr: store.ErrNotFound,
	}
	srv := New(fs, fakeEmbedder{}, nil)
	if rec := do(t, srv, "GET", "/api/v1/projects/missing", "tok", ""); rec.Code != 404 {
		t.Fatalf("get = %d", rec.Code)
	}
	if rec := do(t, srv, "DELETE", "/api/v1/projects/missing", "tok", ""); rec.Code != 404 {
		t.Fatalf("delete = %d", rec.Code)
	}
}
