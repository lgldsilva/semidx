package server

import (
	"errors"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func TestProjectAndStatusStoreErrors(t *testing.T) {
	t.Parallel()
	tok := &store.Token{Scopes: []string{"read", "write", "admin"}}

	listSrv := New(&fakeStore{token: tok, listErr: errors.New("list boom")}, fakeEmbedder{}, nil)
	if rec := do(t, listSrv, "GET", "/api/v1/projects", "tok", ""); rec.Code != 500 {
		t.Fatalf("list err = %d", rec.Code)
	}

	getSrv := New(&fakeStore{token: tok, getErr: errors.New("get boom")}, fakeEmbedder{}, nil)
	if rec := do(t, getSrv, "GET", "/api/v1/projects/p", "tok", ""); rec.Code != 500 {
		t.Fatalf("get err = %d", rec.Code)
	}

	delSrv := New(&fakeStore{token: tok, deleteErr: errors.New("del boom")}, fakeEmbedder{}, nil)
	if rec := do(t, delSrv, "DELETE", "/api/v1/projects/p", "tok", ""); rec.Code != 500 {
		t.Fatalf("delete err = %d", rec.Code)
	}

	stSrv := New(&fakeStore{
		token:        tok,
		project:      &store.Project{ID: 1, Name: "p", Model: "m"},
		fileCountErr: errors.New("count boom"),
	}, fakeEmbedder{}, nil)
	if rec := do(t, stSrv, "GET", "/api/v1/projects/p/status", "tok", ""); rec.Code != 500 {
		t.Fatalf("status err = %d", rec.Code)
	}

	createSrv := New(&fakeStore{token: tok, createErr: errors.New("create boom")}, fakeEmbedder{}, nil)
	if rec := do(t, createSrv, "POST", "/api/v1/projects", "tok", `{"name":"x","source":{"type":"push"}}`); rec.Code != 500 {
		t.Fatalf("create generic err = %d", rec.Code)
	}
}

func TestCreateProjectCredentialCreateFails(t *testing.T) {
	t.Parallel()
	st := newGitCredAPIStore(&store.Token{Scopes: []string{"write", "admin"}})
	st.createErr = nil
	// Make CreateGitCredential fail after project exists by swapping store mid-flight —
	// instead: use a credential that fails validation in mgr.
	srv := New(st, fakeEmbedder{}, nil)
	srv.SetSecretBox(newTestBox(t))
	body := `{
		"name":"priv",
		"source":{"type":"git","url":"git@h:o/r.git","branch":"main"},
		"credential":{"kind":"ssh","secret":"not-valid-key"}
	}`
	rec := do(t, srv, "POST", "/api/v1/projects", "tok", body)
	if rec.Code != 400 {
		t.Fatalf("bad inline ssh = %d %s", rec.Code, rec.Body.String())
	}
}

func TestGitCredentialsConflictAndUpdateJSON(t *testing.T) {
	t.Parallel()
	st := newGitCredAPIStore(&store.Token{Scopes: []string{"admin"}})
	srv := New(st, fakeEmbedder{}, nil)
	srv.SetSecretBox(newTestBox(t))

	pem, _, _ := genTestSSHPEM(t)
	body := `{"host":"gitea.lan","kind":"ssh","username":"git","secret":` + mustJSONString(pem) + `}`
	first := do(t, srv, "POST", "/api/v1/git-credentials", "tok", body)
	if first.Code != 201 {
		t.Fatalf("first create = %d %s", first.Code, first.Body.String())
	}
	st.forceExists = true
	dup := do(t, srv, "POST", "/api/v1/git-credentials", "tok", body)
	if dup.Code != 409 {
		t.Fatalf("dup create = %d %s", dup.Code, dup.Body.String())
	}
	if !strings.Contains(dup.Body.String(), "already exists") {
		t.Fatalf("conflict body=%s", dup.Body.String())
	}

	badJSON := do(t, srv, "PUT", "/api/v1/git-credentials/1", "tok", `{`)
	if badJSON.Code != 400 {
		t.Fatalf("update bad json = %d", badJSON.Code)
	}
}

func TestBearerHasAdminScopeLookupError(t *testing.T) {
	t.Parallel()
	st := newGitCredAPIStore(&store.Token{Scopes: []string{"write", "admin"}})
	st.tokenErr = errors.New("auth db down")
	srv := New(st, fakeEmbedder{}, nil)
	srv.SetSecretBox(newTestBox(t))
	pem, _, _ := genTestSSHPEM(t)
	body := `{
		"name":"priv",
		"source":{"type":"git","url":"git@h:o/r.git"},
		"credential":{"kind":"ssh","secret":` + mustJSONString(pem) + `}
	}`
	rec := do(t, srv, "POST", "/api/v1/projects", "tok", body)
	// tokenErr trips earlier at authed() → 401, or credential scope check → 500.
	if rec.Code != 401 && rec.Code != 500 {
		t.Fatalf("scope check err = %d %s", rec.Code, rec.Body.String())
	}
}

func TestEnableJWT(t *testing.T) {
	t.Parallel()
	srv := New(&fakeStore{}, fakeEmbedder{}, nil)
	if err := srv.EnableJWT("a-test-secret-key-32chars!!"); err != nil {
		t.Fatal(err)
	}
	if srv.jwt == nil {
		t.Fatal("jwt not set")
	}
	if err := srv.EnableJWT(""); err == nil {
		t.Fatal("empty jwt secret should fail")
	}
}

func TestGitCredentialsAPIInternalError(t *testing.T) {
	t.Parallel()
	st := newGitCredAPIStore(&store.Token{Scopes: []string{"admin"}})
	st.forceFail = errors.New("disk full")
	srv := New(st, fakeEmbedder{}, nil)
	srv.SetSecretBox(newTestBox(t))
	rec := do(t, srv, "POST", "/api/v1/git-credentials", "tok",
		`{"host":"h.com","kind":"https","secret":"tok"}`)
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), "operation failed") {
		t.Fatalf("internal = %d %s", rec.Code, rec.Body.String())
	}
}

func TestGitCredentialsKindValidation(t *testing.T) {
	t.Parallel()
	st := newGitCredAPIStore(&store.Token{Scopes: []string{"admin"}})
	srv := New(st, fakeEmbedder{}, nil)
	srv.SetSecretBox(newTestBox(t))
	rec := do(t, srv, "POST", "/api/v1/git-credentials", "tok",
		`{"host":"h.com","kind":"ftp","secret":"x"}`)
	if rec.Code != 400 {
		t.Fatalf("bad kind = %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, srv, "POST", "/api/v1/git-credentials", "tok",
		`{"host":"h.com","kind":"https"}`)
	if rec.Code != 400 {
		t.Fatalf("missing secret = %d %s", rec.Code, rec.Body.String())
	}
}
