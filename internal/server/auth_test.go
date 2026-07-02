package server

import (
	"context"
	"testing"
	"time"

	"github.com/lgldsilva/semidx/internal/jwtauth"
	"github.com/lgldsilva/semidx/internal/passwd"
	"github.com/lgldsilva/semidx/internal/store"
)

func TestResolveScopesJWT(t *testing.T) {
	iss, _ := jwtauth.New("secret")
	minted, _ := iss.Mint("alice", []string{"read", "admin"}, 0, time.Now())

	t.Run("valid JWT authorizes from its claims (jti active)", func(t *testing.T) {
		// TokenByHash returns non-nil → the jti is active (not revoked).
		srv := New(&fakeStore{token: &store.Token{Scopes: []string{"read"}}}, fakeEmbedder{}, nil)
		if err := srv.EnableJWT("secret"); err != nil {
			t.Fatal(err)
		}
		scopes, ok, err := srv.resolveScopes(context.Background(), minted.Token)
		if err != nil || !ok {
			t.Fatalf("resolveScopes = ok %v, err %v; want ok", ok, err)
		}
		// Scopes come from the JWT claims, not the DB row.
		if len(scopes) != 2 || scopes[0] != "read" || scopes[1] != "admin" {
			t.Errorf("scopes = %v; want [read admin]", scopes)
		}
	})

	t.Run("revoked jti is rejected", func(t *testing.T) {
		// TokenByHash returns nil → the jti was revoked/never recorded.
		srv := New(&fakeStore{token: nil}, fakeEmbedder{}, nil)
		if err := srv.EnableJWT("secret"); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := srv.resolveScopes(context.Background(), minted.Token); ok {
			t.Error("resolveScopes accepted a revoked JWT")
		}
	})

	t.Run("JWT signed with another secret falls through to opaque lookup", func(t *testing.T) {
		other, _ := jwtauth.New("different")
		bad, _ := other.Mint("mallory", []string{"admin"}, 0, time.Now())
		// Our server can't verify it as a JWT, so it tries the opaque path; the
		// fake has no matching token → rejected.
		srv := New(&fakeStore{token: nil}, fakeEmbedder{}, nil)
		_ = srv.EnableJWT("secret")
		if _, ok, _ := srv.resolveScopes(context.Background(), bad.Token); ok {
			t.Error("accepted a JWT signed with the wrong secret")
		}
	})
}

func TestEnsureBootstrapAdmin(t *testing.T) {
	t.Run("creates admin on empty server", func(t *testing.T) {
		fs := &fakeStore{userCount: 0}
		srv := New(fs, fakeEmbedder{}, nil)
		name, err := srv.EnsureBootstrapAdmin(context.Background(), "admin", "s3cret")
		if err != nil || name != "admin" {
			t.Fatalf("EnsureBootstrapAdmin = %q, %v; want admin, nil", name, err)
		}
		if fs.created == nil || fs.created.Role != "admin" {
			t.Fatalf("no admin created: %+v", fs.created)
		}
		// The stored hash must verify against the password, not equal it.
		if fs.created.PasswordHash == "s3cret" {
			t.Error("password stored in plaintext")
		}
		if ok, _ := passwd.Verify("s3cret", fs.created.PasswordHash); !ok {
			t.Error("stored hash does not verify")
		}
	})

	t.Run("skips when password empty", func(t *testing.T) {
		fs := &fakeStore{}
		name, err := New(fs, fakeEmbedder{}, nil).EnsureBootstrapAdmin(context.Background(), "admin", "")
		if err != nil || name != "" || fs.created != nil {
			t.Errorf("expected skip; got name=%q err=%v created=%+v", name, err, fs.created)
		}
	})

	t.Run("skips when users already exist", func(t *testing.T) {
		fs := &fakeStore{userCount: 3}
		name, err := New(fs, fakeEmbedder{}, nil).EnsureBootstrapAdmin(context.Background(), "admin", "pw")
		if err != nil || name != "" || fs.created != nil {
			t.Errorf("expected skip; got name=%q err=%v created=%+v", name, err, fs.created)
		}
	})
}
