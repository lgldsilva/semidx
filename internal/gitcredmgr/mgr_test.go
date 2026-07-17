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
	errOn    memCredStoreErrors
}

type memCredStoreErrors struct {
	List    bool
	GetByID bool
	Create  bool
	Update  bool
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
	if m.errOn.Create {
		return nil, errors.New("store create error")
	}
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
	if m.errOn.GetByID {
		return nil, errors.New("store get error")
	}
	if c, ok := m.byID[id]; ok {
		cp := *c
		return &cp, nil
	}
	return nil, store.ErrNotFound
}

func (m *memCredStore) ListGitCredentials(_ context.Context) ([]store.GitCredential, error) {
	if m.errOn.List {
		return nil, errors.New("store list error")
	}
	out := make([]store.GitCredential, 0, len(m.byID))
	for _, c := range m.byID {
		out = append(out, *c)
	}
	return out, nil
}

func (m *memCredStore) UpdateGitCredential(_ context.Context, c *store.GitCredential) error {
	if m.errOn.Update {
		return errors.New("store update error")
	}
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

// ---------------------------------------------------------------------------
// coverage-patch: 2026-07-17 — Additional branch & function coverage
// ---------------------------------------------------------------------------

// badStore implements store.Store but NOT store.GitCredentialStore, for testing
// Supported() and gcs() error paths.
type badStore struct{ store.Store }

func TestSupported_True(t *testing.T) {
	t.Parallel()
	st := newMemCredStore()
	svc := New(st, nil, nil)
	if !svc.Supported() {
		t.Error("Supported() = false, want true (memCredStore implements GitCredentialStore)")
	}
}

func TestSupported_False(t *testing.T) {
	t.Parallel()
	svc := New(badStore{}, nil, nil)
	if svc.Supported() {
		t.Error("Supported() = true, want false (badStore does not implement GitCredentialStore)")
	}
}

func TestCreateForProject(t *testing.T) {
	t.Parallel()
	st := newMemCredStore()
	svc := New(st, testBox(t), nil)
	pid := 1

	// CreateForProject should set ProjectID and call Create.
	created, err := svc.CreateForProject(context.Background(), pid, CreateInput{
		Kind: "https", Username: "user", Secret: "ghp_token123",
	})
	if err != nil {
		t.Fatalf("CreateForProject: %v", err)
	}
	if created.Scope != "project" {
		t.Errorf("Scope = %q, want %q", created.Scope, "project")
	}
	if created.ProjectID == nil || *created.ProjectID != pid {
		t.Errorf("ProjectID = %v, want %d", created.ProjectID, pid)
	}
	if created.ProjectName != "demo" {
		t.Errorf("ProjectName = %q, want %q", created.ProjectName, "demo")
	}
	if !strings.Contains(created.Username, "user") {
		t.Errorf("Username = %q, want to contain 'user'", created.Username)
	}

	// Second call with same project should fail (duplicate).
	_, err = svc.CreateForProject(context.Background(), pid, CreateInput{
		Kind: "https", Username: "user2", Secret: "token456",
	})
	if err == nil || !strings.Contains(err.Error(), "credential already exists") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestUpdate_Success(t *testing.T) {
	t.Parallel()
	st := newMemCredStore()
	svc := New(st, testBox(t), nil)
	pid := 1

	created, err := svc.Create(context.Background(), CreateInput{
		ProjectID: &pid, Kind: "https", Username: "old-user", Secret: "old-token",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := svc.Update(context.Background(), created.ID, UpdateInput{
		Username: "new-user", Secret: "new-token",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Username != "new-user" {
		t.Errorf("Username = %q, want %q", updated.Username, "new-user")
	}
}

func TestUpdate_ChangeKindRequiresSecret(t *testing.T) {
	t.Parallel()
	st := newMemCredStore()
	svc := New(st, testBox(t), nil)
	pid := 1

	created, err := svc.Create(context.Background(), CreateInput{
		ProjectID: &pid, Kind: "https", Username: "user", Secret: "old-token",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Trying to change kind without providing a secret should fail.
	_, err = svc.Update(context.Background(), created.ID, UpdateInput{
		Kind: "ssh",
	})
	if err == nil || !strings.Contains(err.Error(), "secret is required when changing credential kind") {
		t.Fatalf("expected kind-change error, got %v", err)
	}
}

func TestUpdate_BadKind(t *testing.T) {
	t.Parallel()
	st := newMemCredStore()
	svc := New(st, testBox(t), nil)
	pid := 1

	created, err := svc.Create(context.Background(), CreateInput{
		ProjectID: &pid, Kind: "https", Username: "user", Secret: "token",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = svc.Update(context.Background(), created.ID, UpdateInput{
		Kind: "invalid",
	})
	if err == nil || !strings.Contains(err.Error(), "kind must be https or ssh") {
		t.Fatalf("expected bad-kind error, got %v", err)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	t.Parallel()
	st := newMemCredStore()
	svc := New(st, testBox(t), nil)

	_, err := svc.Update(context.Background(), 9999, UpdateInput{
		Username: "who",
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want store.ErrNotFound", err)
	}
}

func TestUpdate_SecretboxDisabled(t *testing.T) {
	t.Parallel()
	st := newMemCredStore()
	svc := New(st, nil, nil) // no secretbox

	_, err := svc.Update(context.Background(), 1, UpdateInput{
		Username: "user",
	})
	if !errors.Is(err, ErrSecretboxDisabled) {
		t.Fatalf("err = %v, want ErrSecretboxDisabled", err)
	}
}

func TestDelete_Success(t *testing.T) {
	t.Parallel()
	st := newMemCredStore()
	svc := New(st, testBox(t), nil)
	pid := 1

	created, err := svc.Create(context.Background(), CreateInput{
		ProjectID: &pid, Kind: "https", Username: "user", Secret: "secret",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Delete(context.Background(), created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify it's gone.
	list, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list {
		if c.ID == created.ID {
			t.Fatal("credential still present after Delete")
		}
	}
}

func TestDelete_NotFound(t *testing.T) {
	t.Parallel()
	st := newMemCredStore()
	svc := New(st, testBox(t), nil)

	err := svc.Delete(context.Background(), 9999)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want store.ErrNotFound", err)
	}
}

func TestDelete_Unsupported(t *testing.T) {
	t.Parallel()
	svc := New(badStore{}, testBox(t), nil)
	err := svc.Delete(context.Background(), 1)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

// TestSanitizeValidationErr exercises sanitizeValidationErr via validateSecret
// with an SSH key that triggers a parse error containing a colon.
func TestSanitizeValidationErr(t *testing.T) {
	t.Parallel()
	// The string "-----BEGIN INVALID KEY-----" is not a valid PEM block;
	// ssh.ParsePrivateKey returns an error that contains a colon.
	st := newMemCredStore()
	svc := New(st, testBox(t), nil)

	_, err := svc.Create(context.Background(), CreateInput{
		Host: "github.com", Kind: "ssh", Secret: "not-a-key-at-all",
	})
	if err == nil {
		t.Fatal("expected error for invalid SSH key")
	}
	// The error should be wrapped but the underlying validation message
	// should mention the key being invalid (sanitized).
	if !strings.Contains(err.Error(), "invalid SSH private key") {
		t.Errorf("error = %q, want 'invalid SSH private key' prefix", err)
	}
}

func TestCreate_DuplicateProject(t *testing.T) {
	t.Parallel()
	st := newMemCredStore()
	svc := New(st, testBox(t), nil)
	pid := 1

	_, err := svc.Create(context.Background(), CreateInput{
		ProjectID: &pid, Kind: "https", Username: "user", Secret: "token1",
	})
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}

	// Duplicate for same project.
	_, err = svc.Create(context.Background(), CreateInput{
		ProjectID: &pid, Kind: "https", Username: "user2", Secret: "token2",
	})
	if err == nil || !strings.Contains(err.Error(), "credential already exists") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestCreate_DuplicateHost(t *testing.T) {
	t.Parallel()
	st := newMemCredStore()
	svc := New(st, testBox(t), nil)

	_, err := svc.Create(context.Background(), CreateInput{
		Host: "github.com", Kind: "https", Username: "user", Secret: "token1",
	})
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}

	// Duplicate for same host (case-insensitive).
	_, err = svc.Create(context.Background(), CreateInput{
		Host: "GITHUB.COM", Kind: "https", Username: "user2", Secret: "token2",
	})
	if err == nil || !strings.Contains(err.Error(), "credential already exists") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// coverage-patch: 2026-07-17 — wave 2: cover remaining ~21 statements
// ---------------------------------------------------------------------------

func TestCreate_UnsupportedStore(t *testing.T) {
	t.Parallel()
	svc := New(badStore{}, testBox(t), nil)
	_, err := svc.Create(context.Background(), CreateInput{
		Host: "github.com", Kind: "https", Username: "u", Secret: "tok",
	})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestCreate_DisabledSecretBox(t *testing.T) {
	t.Parallel()
	svc := New(newMemCredStore(), nil, nil) // no secretbox
	_, err := svc.Create(context.Background(), CreateInput{
		Host: "github.com", Kind: "https", Username: "u", Secret: "tok",
	})
	if !errors.Is(err, ErrSecretboxDisabled) {
		t.Fatalf("err = %v, want ErrSecretboxDisabled", err)
	}
}

func TestCreate_BothProjectAndHost(t *testing.T) {
	t.Parallel()
	svc := New(newMemCredStore(), testBox(t), nil)
	_, err := svc.Create(context.Background(), CreateInput{
		ProjectID: intPtr(1), Host: "github.com", Kind: "https", Username: "u", Secret: "tok",
	})
	if err == nil || !strings.Contains(err.Error(), "set exactly one") {
		t.Fatalf("expected 'set exactly one' error, got %v", err)
	}
}

func TestCreate_NeitherProjectNorHost(t *testing.T) {
	t.Parallel()
	svc := New(newMemCredStore(), testBox(t), nil)
	_, err := svc.Create(context.Background(), CreateInput{
		Kind: "https", Username: "u", Secret: "tok",
	})
	if err == nil || !strings.Contains(err.Error(), "set exactly one") {
		t.Fatalf("expected 'set exactly one' error, got %v", err)
	}
}

func TestCreate_InvalidKind(t *testing.T) {
	t.Parallel()
	svc := New(newMemCredStore(), testBox(t), nil)
	_, err := svc.Create(context.Background(), CreateInput{
		Host: "github.com", Kind: "badkind", Username: "u", Secret: "tok",
	})
	if err == nil || !strings.Contains(err.Error(), "kind must be https or ssh") {
		t.Fatalf("expected 'kind must be https or ssh' error, got %v", err)
	}
}

func TestCreate_EmptySecret(t *testing.T) {
	t.Parallel()
	svc := New(newMemCredStore(), testBox(t), nil)
	_, err := svc.Create(context.Background(), CreateInput{
		Host: "github.com", Kind: "https", Username: "u", Secret: "",
	})
	if err == nil || !strings.Contains(err.Error(), "secret is required") {
		t.Fatalf("expected 'secret is required' error, got %v", err)
	}
}

func TestCreate_StoreError(t *testing.T) {
	t.Parallel()
	st := newMemCredStore()
	st.errOn.Create = true
	svc := New(st, testBox(t), nil)
	_, err := svc.Create(context.Background(), CreateInput{
		Host: "github.com", Kind: "https", Username: "u", Secret: "tok",
	})
	if err == nil || !strings.Contains(err.Error(), "store create error") {
		t.Fatalf("expected store error, got %v", err)
	}
}

func TestList_StoreError(t *testing.T) {
	t.Parallel()
	st := newMemCredStore()
	st.errOn.List = true
	svc := New(st, testBox(t), nil)
	_, err := svc.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "store list error") {
		t.Fatalf("expected store error, got %v", err)
	}
}

func TestUpdate_UnsupportedStore(t *testing.T) {
	t.Parallel()
	svc := New(badStore{}, testBox(t), nil)
	_, err := svc.Update(context.Background(), 1, UpdateInput{Username: "u"})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestUpdate_NoSecretNoKindChange(t *testing.T) {
	t.Parallel()
	st := newMemCredStore()
	svc := New(st, testBox(t), nil)
	created, err := svc.Create(context.Background(), CreateInput{
		ProjectID: intPtr(1), Kind: "https", Username: "old-user", Secret: "old-token",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Update with no secret and no kind change — exercises the noop return
	// in applyUpdateSecret (line 169).
	updated, err := svc.Update(context.Background(), created.ID, UpdateInput{
		Username: "new-user", // no Secret, no Kind
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Username != "new-user" {
		t.Errorf("Username = %q, want %q", updated.Username, "new-user")
	}
}

func TestUpdate_StoreErrorOnUpdate(t *testing.T) {
	t.Parallel()
	st := newMemCredStore()
	svc := New(st, testBox(t), nil)
	created, err := svc.Create(context.Background(), CreateInput{
		ProjectID: intPtr(1), Kind: "https", Username: "u", Secret: "token",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Make UpdateGitCredential fail (not GetGitCredentialByID).
	st.errOn.Update = true
	_, err = svc.Update(context.Background(), created.ID, UpdateInput{
		Username: "new-user", Secret: "new-token",
	})
	if err == nil || !strings.Contains(err.Error(), "store update error") {
		t.Fatalf("expected store update error, got %v", err)
	}
}

func TestUpdate_StoreErrorOnGetByID(t *testing.T) {
	t.Parallel()
	st := newMemCredStore()
	svc := New(st, testBox(t), nil)
	created, err := svc.Create(context.Background(), CreateInput{
		ProjectID: intPtr(1), Kind: "https", Username: "u", Secret: "token",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	st.errOn.GetByID = true

	// First call hits errOn.GetByID during the initial GetGitCredentialByID.
	_, err = svc.Update(context.Background(), created.ID, UpdateInput{
		Username: "new-user", Secret: "new-token",
	})
	if err == nil || !strings.Contains(err.Error(), "store get error") {
		t.Fatalf("expected store get error, got %v", err)
	}
}

// getByIDOnceAfterUpdate wraps memCredStore to return an error from
// GetGitCredentialByID on the second call (simulating a post-update re-fetch
// failure). UpdateGitCredential is delegated so it succeeds.
type getByIDOnceAfterUpdate struct {
	*memCredStore
	getCalls int
}

func (s *getByIDOnceAfterUpdate) GetGitCredentialByID(ctx context.Context, id int) (*store.GitCredential, error) {
	s.getCalls++
	if s.getCalls > 1 {
		return nil, errors.New("post-update store get error")
	}
	return s.memCredStore.GetGitCredentialByID(ctx, id)
}

func TestUpdate_StoreErrorOnPostUpdateGet(t *testing.T) {
	t.Parallel()
	inner := newMemCredStore()
	st := &getByIDOnceAfterUpdate{memCredStore: inner}
	svc := New(st, testBox(t), nil)

	created, err := svc.Create(context.Background(), CreateInput{
		ProjectID: intPtr(1), Kind: "https", Username: "u", Secret: "token",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// svc.Update does: Get → applyUpdateSecret → UpdateGitCredential → Get.
	// First GetGitCredentialByID succeeds (getCalls=1), then the re-fetch
	// fails because getCalls > 1.
	_, err = svc.Update(context.Background(), created.ID, UpdateInput{
		Username: "new-user", Secret: "new-token",
	})
	if err == nil || !strings.Contains(err.Error(), "post-update store get error") {
		t.Fatalf("expected post-update get error, got %v", err)
	}
}

func TestUpdate_ValidateSecretError(t *testing.T) {
	t.Parallel()
	st := newMemCredStore()
	svc := New(st, testBox(t), nil)
	pid := 1
	created, err := svc.Create(context.Background(), CreateInput{
		ProjectID: &pid, Kind: "https", Username: "u", Secret: "token",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Update with bad SSH key while providing a secret — exercises
	// applyUpdateSecret's validateSecret call.
	_, err = svc.Update(context.Background(), created.ID, UpdateInput{
		Kind: "ssh", Secret: "not-a-valid-ssh-key",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid SSH private key") {
		t.Fatalf("expected SSH validation error, got %v", err)
	}
}

func TestSSHFingerprint_InvalidKey(t *testing.T) {
	t.Parallel()
	if got := sshFingerprint([]byte("not-a-pem")); got != "" {
		t.Errorf("sshFingerprint = %q, want empty string for invalid key", got)
	}
}

func TestToPublic_SSHOpenError(t *testing.T) {
	t.Parallel()
	st := newMemCredStore()
	svc := New(st, testBox(t), nil)

	// Manually insert an SSH credential with corrupted SecretEnc.
	// toPublic will try to s.secrets.Open(c.SecretEnc) which will fail,
	// log the error, and return nil fingerprint (not a fatal error).
	corrupted := &store.GitCredential{
		ID: 1, ProjectID: intPtr(1), Host: "", Kind: "ssh",
		Username: "git", SecretEnc: []byte("not-valid-encrypted-data"),
		KeyVersion: 1,
	}
	st.byID[1] = corrupted

	list, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v — expected success even with decrypt error", err)
	}
	if len(list) == 0 {
		t.Fatal("expected at least 1 credential")
	}
	if list[0].SSHFingerprint != "" {
		t.Errorf("expected empty fingerprint on decrypt failure, got %q", list[0].SSHFingerprint)
	}
}

func intPtr(n int) *int { return &n }

// coverage-patch: 2026-07-17 — end of patch
