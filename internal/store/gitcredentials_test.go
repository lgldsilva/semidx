package store

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func intPtr(v int) *int { return &v }

func TestGitCredentialCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	pid, err := s.UpsertProject(ctx, "creds-demo", "/tmp/creds-demo", "bge-m3", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	// Create a project-scoped credential.
	c, err := s.CreateGitCredential(ctx, &GitCredential{
		ProjectID: intPtr(pid),
		Kind:      "https",
		Username:  "bot",
		SecretEnc: []byte("ciphertext-v1"),
		Label:     "deploy token",
	})
	if err != nil {
		t.Fatalf("CreateGitCredential: %v", err)
	}
	if c.ID == 0 || c.ProjectID == nil || *c.ProjectID != pid || c.KeyVersion != 1 {
		t.Fatalf("created credential = %+v (want project scope, key_version default 1)", c)
	}
	if c.CreatedAt.IsZero() || c.UpdatedAt.IsZero() {
		t.Fatalf("timestamps not populated: %+v", c)
	}

	// Get by project.
	got, err := s.GetGitCredentialForProject(ctx, pid)
	if err != nil {
		t.Fatalf("GetGitCredentialForProject: %v", err)
	}
	if got.ID != c.ID || !bytes.Equal(got.SecretEnc, []byte("ciphertext-v1")) || got.Label != "deploy token" {
		t.Errorf("GetGitCredentialForProject = %+v", got)
	}

	// Create a host-scoped credential and list both.
	h, err := s.CreateGitCredential(ctx, &GitCredential{
		Host:          "git.example.com",
		Kind:          "ssh",
		Username:      "git",
		SecretEnc:     []byte("ssh-key-ciphertext"),
		KeyVersion:    2,
		SSHKnownHosts: "git.example.com ssh-ed25519 AAAA",
	})
	if err != nil {
		t.Fatalf("CreateGitCredential (host): %v", err)
	}
	if h.ProjectID != nil || h.Host != "git.example.com" || h.KeyVersion != 2 {
		t.Fatalf("host credential = %+v", h)
	}
	list, err := s.ListGitCredentials(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListGitCredentials = %d entries, err %v", len(list), err)
	}
	if list[0].ID != h.ID || list[1].ID != c.ID {
		t.Errorf("list order = [%d %d], want host-scoped first [%d %d]",
			list[0].ID, list[1].ID, h.ID, c.ID)
	}

	// Update replaces secret + known hosts and bumps updated_at.
	time.Sleep(20 * time.Millisecond) // ensure NOW() advances past created_at
	h.SecretEnc = []byte("rotated-ciphertext")
	h.KeyVersion = 3
	h.SSHKnownHosts = "git.example.com ssh-ed25519 BBBB"
	h.Label = "rotated"
	if err := s.UpdateGitCredential(ctx, h); err != nil {
		t.Fatalf("UpdateGitCredential: %v", err)
	}
	upd, err := s.GetGitCredentialForHost(ctx, "git.example.com")
	if err != nil {
		t.Fatalf("GetGitCredentialForHost after update: %v", err)
	}
	if !bytes.Equal(upd.SecretEnc, []byte("rotated-ciphertext")) || upd.KeyVersion != 3 ||
		upd.SSHKnownHosts != "git.example.com ssh-ed25519 BBBB" || upd.Label != "rotated" {
		t.Errorf("credential after update = %+v", upd)
	}
	if !upd.UpdatedAt.After(upd.CreatedAt) {
		t.Errorf("updated_at %v not after created_at %v", upd.UpdatedAt, upd.CreatedAt)
	}

	// Delete.
	if err := s.DeleteGitCredential(ctx, h.ID); err != nil {
		t.Fatalf("DeleteGitCredential: %v", err)
	}
	if _, err := s.GetGitCredentialForHost(ctx, "git.example.com"); !errors.Is(err, ErrNotFound) {
		t.Errorf("get after delete = %v, want ErrNotFound", err)
	}
}

func TestGitCredentialScopeUniqueness(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	pid, err := s.UpsertProject(ctx, "creds-uniq", "/tmp/creds-uniq", "bge-m3", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if _, err := s.CreateGitCredential(ctx, &GitCredential{
		ProjectID: intPtr(pid), Kind: "https", SecretEnc: []byte("s1"),
	}); err != nil {
		t.Fatalf("first project credential: %v", err)
	}

	// Second credential for the same project is rejected.
	if _, err := s.CreateGitCredential(ctx, &GitCredential{
		ProjectID: intPtr(pid), Kind: "ssh", SecretEnc: []byte("s2"),
	}); !errors.Is(err, ErrCredentialExists) {
		t.Errorf("duplicate project credential = %v, want ErrCredentialExists", err)
	}

	// Host uniqueness is case-insensitive.
	if _, err := s.CreateGitCredential(ctx, &GitCredential{
		Host: "GitHub.com", Kind: "https", SecretEnc: []byte("s3"),
	}); err != nil {
		t.Fatalf("first host credential: %v", err)
	}
	if _, err := s.CreateGitCredential(ctx, &GitCredential{
		Host: "github.com", Kind: "https", SecretEnc: []byte("s4"),
	}); !errors.Is(err, ErrCredentialExists) {
		t.Errorf("case-variant host credential = %v, want ErrCredentialExists", err)
	}

	// And host lookup matches case-insensitively.
	got, err := s.GetGitCredentialForHost(ctx, "GITHUB.COM")
	if err != nil || got.Host != "GitHub.com" {
		t.Errorf("GetGitCredentialForHost(GITHUB.COM) = %+v, err %v", got, err)
	}
}

func TestGitCredentialValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	pid, err := s.UpsertProject(ctx, "creds-val", "/tmp/creds-val", "bge-m3", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	cases := []struct {
		name string
		cred GitCredential
	}{
		{"no scope", GitCredential{Kind: "https", SecretEnc: []byte("s")}},
		{"both scopes", GitCredential{ProjectID: intPtr(pid), Host: "h.example.com", Kind: "https", SecretEnc: []byte("s")}},
		{"bad kind", GitCredential{Host: "h.example.com", Kind: "token", SecretEnc: []byte("s")}},
		{"empty secret", GitCredential{Host: "h.example.com", Kind: "https"}},
	}
	for _, tc := range cases {
		if _, err := s.CreateGitCredential(ctx, &tc.cred); err == nil {
			t.Errorf("%s: CreateGitCredential succeeded, want error", tc.name)
		}
	}

	// The scope CHECK constraint also holds at the SQL layer (defense in depth
	// against writers that bypass CreateGitCredential).
	_, err = s.pool.Exec(ctx, `
		INSERT INTO git_credentials (project_id, host, kind, secret_enc)
		VALUES ($1, 'both.example.com', 'https', $2)`, pid, []byte("s"))
	if err == nil {
		t.Error("direct insert with both scopes succeeded, want CHECK violation")
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO git_credentials (project_id, host, kind, secret_enc)
		VALUES (NULL, '', 'https', $1)`, []byte("s"))
	if err == nil {
		t.Error("direct insert with no scope succeeded, want CHECK violation")
	}
}

func TestGitCredentialProjectCascade(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	pid, err := s.UpsertProject(ctx, "creds-cascade", "/tmp/creds-cascade", "bge-m3", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if _, err := s.CreateGitCredential(ctx, &GitCredential{
		ProjectID: intPtr(pid), Kind: "https", SecretEnc: []byte("s"),
	}); err != nil {
		t.Fatalf("CreateGitCredential: %v", err)
	}
	if err := s.DeleteProject(ctx, "creds-cascade"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := s.GetGitCredentialForProject(ctx, pid); !errors.Is(err, ErrNotFound) {
		t.Errorf("credential after project delete = %v, want ErrNotFound (cascade)", err)
	}
	if list, err := s.ListGitCredentials(ctx); err != nil || len(list) != 0 {
		t.Errorf("ListGitCredentials after cascade = %d entries, err %v", len(list), err)
	}
}

func TestGitCredentialNotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.GetGitCredentialForProject(ctx, 424242); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetGitCredentialForProject = %v, want ErrNotFound", err)
	}
	if _, err := s.GetGitCredentialForHost(ctx, "nowhere.example.com"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetGitCredentialForHost = %v, want ErrNotFound", err)
	}
	if err := s.UpdateGitCredential(ctx, &GitCredential{
		ID: 424242, Kind: "https", SecretEnc: []byte("s"),
	}); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateGitCredential = %v, want ErrNotFound", err)
	}
	if err := s.DeleteGitCredential(ctx, 424242); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteGitCredential = %v, want ErrNotFound", err)
	}
}
