package store

import (
	"context"
	"errors"
	"testing"
)

// Regression: a project id from the request was stored on a credential
// without checking it belongs to the caller's tenant.
func TestCreateGitCredentialRejectsForeignProject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	otherCtx := otherTenantCtx(t, s)
	theirs, err := s.UpsertProject(otherCtx, "theirs", "/theirs", "m", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	_, err = s.CreateGitCredential(ctx, &GitCredential{
		ProjectID: intPtr(theirs), Kind: "https", SecretEnc: []byte("x"),
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("CreateGitCredential(foreign project) err = %v, want ErrNotFound", err)
	}
	if _, err := s.CreateGitCredential(otherCtx, &GitCredential{
		ProjectID: intPtr(theirs), Kind: "https", SecretEnc: []byte("x"),
	}); err != nil {
		t.Fatalf("CreateGitCredential(own project): %v", err)
	}
}
