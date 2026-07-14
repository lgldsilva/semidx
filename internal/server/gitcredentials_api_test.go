package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/lgldsilva/semidx/internal/store"
)

type gitCredAPIStore struct {
	*credStore
	creds map[int]*store.GitCredential
	next  int
}

func newGitCredAPIStore(token *store.Token) *gitCredAPIStore {
	return &gitCredAPIStore{
		credStore: &credStore{fakeStore: &fakeStore{token: token}},
		creds:     map[int]*store.GitCredential{},
	}
}

func (g *gitCredAPIStore) CreateGitCredential(_ context.Context, c *store.GitCredential) (*store.GitCredential, error) {
	g.next++
	out := *c
	out.ID = g.next
	g.creds[out.ID] = &out
	return &out, nil
}

func (g *gitCredAPIStore) GetGitCredentialByID(_ context.Context, id int) (*store.GitCredential, error) {
	if c, ok := g.creds[id]; ok {
		cp := *c
		return &cp, nil
	}
	return nil, store.ErrNotFound
}

func (g *gitCredAPIStore) ListGitCredentials(_ context.Context) ([]store.GitCredential, error) {
	out := make([]store.GitCredential, 0, len(g.creds))
	for _, c := range g.creds {
		out = append(out, *c)
	}
	return out, nil
}

func (g *gitCredAPIStore) UpdateGitCredential(_ context.Context, c *store.GitCredential) error {
	if _, ok := g.creds[c.ID]; !ok {
		return store.ErrNotFound
	}
	cp := *c
	g.creds[c.ID] = &cp
	return nil
}

func (g *gitCredAPIStore) DeleteGitCredential(_ context.Context, id int) error {
	if _, ok := g.creds[id]; !ok {
		return store.ErrNotFound
	}
	delete(g.creds, id)
	return nil
}

func genTestSSHPEM(t *testing.T) (pemStr, secret string, fingerprint string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pemBlock, err := ssh.MarshalPrivateKey(priv, "test")
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(pemBlock)), "super-secret-pem-body", ssh.FingerprintSHA256(signer.PublicKey())
}

func TestGitCredentialsAPINeverLeaksSecret(t *testing.T) {
	t.Parallel()
	st := newGitCredAPIStore(&store.Token{Scopes: []string{"admin"}})
	box := newTestBox(t)
	srv := New(st, fakeEmbedder{}, nil)
	srv.SetSecretBox(box)

	pem, plainSecret, wantFP := genTestSSHPEM(t)
	body := `{"host":"gitea.lan","kind":"ssh","username":"git","secret":` + mustJSONString(pem) + `,"label":"deploy"}`
	rec := do(t, srv, "POST", "/api/v1/git-credentials", "tok", body)
	if rec.Code != 201 {
		t.Fatalf("create status = %d body %s", rec.Code, rec.Body.String())
	}
	resp := rec.Body.String()
	if strings.Contains(resp, pem) || strings.Contains(resp, plainSecret) || strings.Contains(resp, "BEGIN OPENSSH") {
		t.Fatalf("create response leaks secret: %s", resp)
	}
	if !strings.Contains(resp, wantFP) {
		t.Fatalf("create response missing fingerprint: %s", resp)
	}

	rec = do(t, srv, "GET", "/api/v1/git-credentials", "tok", "")
	if rec.Code != 200 {
		t.Fatalf("list status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), pem) || strings.Contains(rec.Body.String(), plainSecret) {
		t.Fatalf("list leaks secret: %s", rec.Body.String())
	}

	rec = do(t, srv, "PUT", "/api/v1/git-credentials/1", "tok", `{"kind":"ssh","username":"git","label":"x"}`)
	if rec.Code != 200 {
		t.Fatalf("update status = %d body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), pem) {
		t.Fatalf("update leaks secret: %s", rec.Body.String())
	}

	if rec := do(t, srv, "DELETE", "/api/v1/git-credentials/1", "tok", ""); rec.Code != 204 {
		t.Fatalf("delete status = %d", rec.Code)
	}
}

func TestGitCredentialsAPIRequiresAdminScope(t *testing.T) {
	t.Parallel()
	st := newGitCredAPIStore(&store.Token{Scopes: []string{"read", "write"}})
	srv := New(st, fakeEmbedder{}, nil)
	srv.SetSecretBox(newTestBox(t))

	if rec := do(t, srv, "GET", "/api/v1/git-credentials", "tok", ""); rec.Code != 403 {
		t.Fatalf("list without admin = %d", rec.Code)
	}
	if rec := do(t, srv, "POST", "/api/v1/git-credentials", "tok", `{"host":"x.com","kind":"https","secret":"tok"}`); rec.Code != 403 {
		t.Fatalf("create without admin = %d", rec.Code)
	}
}

func TestGitCredentialsAPIPassphraseProtectedSSH(t *testing.T) {
	t.Parallel()
	st := newGitCredAPIStore(&store.Token{Scopes: []string{"admin"}})
	srv := New(st, fakeEmbedder{}, nil)
	srv.SetSecretBox(newTestBox(t))

	// Invalid blob still triggers ssh parse failure; real passphrase case covered in gitcredmgr.
	rec := do(t, srv, "POST", "/api/v1/git-credentials", "tok",
		`{"host":"x.com","kind":"ssh","secret":"not-a-key"}`)
	if rec.Code != 400 {
		t.Fatalf("invalid ssh status = %d body %s", rec.Code, rec.Body.String())
	}
}

func mustJSONString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// Ensure gitCredAPIStore satisfies GitCredentialStore at compile time.
var _ store.GitCredentialStore = (*gitCredAPIStore)(nil)
