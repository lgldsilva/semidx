package gitcredmgr

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/lgldsilva/semidx/internal/secretbox"
	"github.com/lgldsilva/semidx/internal/store"
)

type memCredStore struct {
	store.Store
	byID     map[int]*store.GitCredential
	nextID   int
	projects map[int]*store.Project
}

func newMemCredStore() *memCredStore {
	return &memCredStore{
		byID:     map[int]*store.GitCredential{},
		projects: map[int]*store.Project{1: {ID: 1, Name: "demo"}},
	}
}

func (m *memCredStore) GetProjectByID(_ context.Context, id int) (*store.Project, error) {
	if p, ok := m.projects[id]; ok {
		return p, nil
	}
	return nil, store.ErrNotFound
}

func (m *memCredStore) CreateGitCredential(_ context.Context, c *store.GitCredential) (*store.GitCredential, error) {
	for _, existing := range m.byID {
		if c.ProjectID != nil && existing.ProjectID != nil && *existing.ProjectID == *c.ProjectID {
			return nil, store.ErrCredentialExists
		}
		if c.Host != "" && existing.ProjectID == nil && strings.EqualFold(existing.Host, c.Host) {
			return nil, store.ErrCredentialExists
		}
	}
	m.nextID++
	out := *c
	out.ID = m.nextID
	m.byID[out.ID] = &out
	return &out, nil
}

func (m *memCredStore) GetGitCredentialByID(_ context.Context, id int) (*store.GitCredential, error) {
	if c, ok := m.byID[id]; ok {
		cp := *c
		return &cp, nil
	}
	return nil, store.ErrNotFound
}

func (m *memCredStore) ListGitCredentials(_ context.Context) ([]store.GitCredential, error) {
	out := make([]store.GitCredential, 0, len(m.byID))
	for _, c := range m.byID {
		out = append(out, *c)
	}
	return out, nil
}

func (m *memCredStore) UpdateGitCredential(_ context.Context, c *store.GitCredential) error {
	if _, ok := m.byID[c.ID]; !ok {
		return store.ErrNotFound
	}
	cp := *c
	m.byID[c.ID] = &cp
	return nil
}

func (m *memCredStore) DeleteGitCredential(_ context.Context, id int) error {
	if _, ok := m.byID[id]; !ok {
		return store.ErrNotFound
	}
	delete(m.byID, id)
	return nil
}

func (m *memCredStore) GetGitCredentialForProject(context.Context, int) (*store.GitCredential, error) {
	return nil, store.ErrNotFound
}
func (m *memCredStore) GetGitCredentialForHost(context.Context, string) (*store.GitCredential, error) {
	return nil, store.ErrNotFound
}

func testBox(t *testing.T) *secretbox.Box {
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

func genSSHKeyPEM(t *testing.T) (string, string) {
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
	return string(pem.EncodeToMemory(pemBlock)), ssh.FingerprintSHA256(signer.PublicKey())
}

func TestCreateListSSHFingerprint(t *testing.T) {
	t.Parallel()
	st := newMemCredStore()
	svc := New(st, testBox(t), nil)
	pem, wantFP := genSSHKeyPEM(t)
	const token = "not-in-response"
	pid := 1

	created, err := svc.Create(context.Background(), CreateInput{
		ProjectID: &pid, Kind: "ssh", Username: "git", Secret: pem, Label: "deploy",
		SSHKnownHosts: "git.example.com ssh-ed25519 AAAA",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.SSHFingerprint != wantFP {
		t.Errorf("fingerprint = %q, want %q", created.SSHFingerprint, wantFP)
	}
	if created.Scope != "project" || created.ProjectName != "demo" {
		t.Errorf("created = %+v", created)
	}

	list, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d", len(list))
	}
	body := mustJSON(t, list)
	if strings.Contains(body, pem) || strings.Contains(body, token) || strings.Contains(body, "BEGIN") {
		t.Errorf("list JSON leaks secret: %s", body)
	}
	if !strings.Contains(body, wantFP) {
		t.Errorf("list JSON missing fingerprint: %s", body)
	}
}

func TestValidatePassphraseProtectedSSH(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed")
	}
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_ed25519")
	if out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-f", keyPath, "-N", "secret", "-q").CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	pem, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	svc := New(newMemCredStore(), testBox(t), nil)
	_, err = svc.Create(context.Background(), CreateInput{
		Host: "github.com", Kind: "ssh", Secret: string(pem),
	})
	if err == nil || !strings.Contains(err.Error(), "passphrase-protected") {
		t.Fatalf("Create encrypted key err = %v, want passphrase rejection", err)
	}
}

func TestUnsupportedStore(t *testing.T) {
	t.Parallel()
	svc := New(struct{ store.Store }{}, testBox(t), nil)
	_, err := svc.List(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
