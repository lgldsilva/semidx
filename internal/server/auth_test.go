package server

import (
	"context"
	"testing"

	"github.com/lgldsilva/semidx/internal/passwd"
)

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
