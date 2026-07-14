package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/secretbox"
	"github.com/lgldsilva/semidx/internal/store"
)

// credStore layers store.GitCredentialStore over fakeStore so tests can drive
// the three credential-resolution levels (project → host → env).
type credStore struct {
	*fakeStore
	projCred *store.GitCredential
	projErr  error // non-nil overrides projCred (nil projCred → ErrNotFound)
	hostCred *store.GitCredential
	hostErr  error
	gotHost  string // last host asked of GetGitCredentialForHost
}

func (c *credStore) CreateGitCredential(context.Context, *store.GitCredential) (*store.GitCredential, error) {
	return nil, errors.New("not implemented")
}

func (c *credStore) GetGitCredentialForProject(_ context.Context, _ int) (*store.GitCredential, error) {
	if c.projErr != nil {
		return nil, c.projErr
	}
	if c.projCred == nil {
		return nil, store.ErrNotFound
	}
	return c.projCred, nil
}

func (c *credStore) GetGitCredentialForHost(_ context.Context, host string) (*store.GitCredential, error) {
	c.gotHost = host
	if c.hostErr != nil {
		return nil, c.hostErr
	}
	if c.hostCred == nil {
		return nil, store.ErrNotFound
	}
	return c.hostCred, nil
}

func (c *credStore) ListGitCredentials(context.Context) ([]store.GitCredential, error) {
	return nil, nil
}
func (c *credStore) UpdateGitCredential(context.Context, *store.GitCredential) error { return nil }
func (c *credStore) DeleteGitCredential(context.Context, int) error                  { return nil }

// newTestBox builds a real secretbox from a random runtime-generated key —
// never a checked-in secret.
func newTestBox(t *testing.T) *secretbox.Box {
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

func sealSecret(t *testing.T, box *secretbox.Box, plain string) []byte {
	t.Helper()
	blob, err := box.Seal([]byte(plain))
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func gitProject() *store.Project {
	return &store.Project{
		ID: 1, Name: "p", SourceType: "git",
		GitURL: "https://gitea.lan/owner/repo.git", Branch: "main",
	}
}

func TestResolveGitOptionsProjectCredentialWins(t *testing.T) {
	t.Parallel()
	box := newTestBox(t)
	cs := &credStore{
		fakeStore: &fakeStore{},
		projCred: &store.GitCredential{
			ID: 7, Kind: "https", Username: "bot",
			SecretEnc: sealSecret(t, box, "proj-token"),
		},
		hostCred: &store.GitCredential{
			ID: 8, Kind: "https", Username: "other",
			SecretEnc: sealSecret(t, box, "host-token"),
		},
	}
	srv := New(cs, fakeEmbedder{}, nil)
	srv.SetGitAllowFile(true)
	srv.SetGitAuth(true, "env-token", "env-user")
	srv.SetSecretBox(box)

	opts, err := srv.resolveGitOptions(context.Background(), gitProject(), "/data")
	if err != nil {
		t.Fatalf("resolveGitOptions() error = %v", err)
	}
	if opts.Token != "proj-token" || opts.TokenUser != "bot" {
		t.Errorf("token = %q/%q, want proj-token/bot", opts.Token, opts.TokenUser)
	}
	if cs.gotHost != "" {
		t.Errorf("host lookup ran (%q) despite a project credential", cs.gotHost)
	}
	if opts.DataDir != "/data" || opts.Name != "p" || opts.URL != "https://gitea.lan/owner/repo.git" ||
		opts.Branch != "main" || !opts.AllowFileURL || !opts.SSLNoVerify {
		t.Errorf("base options not propagated: %+v", opts)
	}
}

func TestResolveGitOptionsHostCredentialSSH(t *testing.T) {
	t.Parallel()
	box := newTestBox(t)
	cs := &credStore{
		fakeStore: &fakeStore{},
		hostCred: &store.GitCredential{
			ID: 9, Kind: "ssh", Username: "git",
			SecretEnc:     sealSecret(t, box, "fake-runtime-key-material"),
			SSHKnownHosts: "gitea.lan ssh-ed25519 AAAA",
		},
	}
	srv := New(cs, fakeEmbedder{}, nil)
	srv.SetGitAuth(false, "env-token", "env-user")
	srv.SetSecretBox(box)

	opts, err := srv.resolveGitOptions(context.Background(), gitProject(), "/data")
	if err != nil {
		t.Fatalf("resolveGitOptions() error = %v", err)
	}
	if cs.gotHost != "gitea.lan" {
		t.Errorf("host lookup = %q, want gitea.lan", cs.gotHost)
	}
	if string(opts.SSHKey) != "fake-runtime-key-material" {
		t.Errorf("SSHKey = %q", opts.SSHKey)
	}
	if opts.SSHUser != "git" || opts.SSHKnownHosts != "gitea.lan ssh-ed25519 AAAA" {
		t.Errorf("ssh fields = %q/%q", opts.SSHUser, opts.SSHKnownHosts)
	}
	if opts.Token != "" || opts.TokenUser != "" {
		t.Errorf("https token set (%q/%q) for an ssh credential", opts.Token, opts.TokenUser)
	}
}

func TestResolveGitOptionsEnvFallback(t *testing.T) {
	t.Parallel()
	cs := &credStore{fakeStore: &fakeStore{}} // no stored credentials at all
	srv := New(cs, fakeEmbedder{}, nil)
	srv.SetGitAuth(false, "env-token", "env-user")
	srv.SetSecretBox(newTestBox(t))

	opts, err := srv.resolveGitOptions(context.Background(), gitProject(), "/data")
	if err != nil {
		t.Fatalf("resolveGitOptions() error = %v", err)
	}
	if opts.Token != "env-token" || opts.TokenUser != "env-user" {
		t.Errorf("token = %q/%q, want the env fallback", opts.Token, opts.TokenUser)
	}
}

func TestResolveGitOptionsStoreWithoutCredentialSupport(t *testing.T) {
	t.Parallel()
	srv := New(&fakeStore{}, fakeEmbedder{}, nil) // fakeStore: no GitCredentialStore
	srv.SetGitAuth(false, "env-token", "env-user")

	opts, err := srv.resolveGitOptions(context.Background(), gitProject(), "/data")
	if err != nil {
		t.Fatalf("resolveGitOptions() error = %v", err)
	}
	if opts.Token != "env-token" || opts.TokenUser != "env-user" {
		t.Errorf("token = %q/%q, want the env fallback", opts.Token, opts.TokenUser)
	}
}

func TestResolveGitOptionsUnparseableHostDegradesToEnv(t *testing.T) {
	t.Parallel()
	cs := &credStore{fakeStore: &fakeStore{}}
	srv := New(cs, fakeEmbedder{}, nil)
	srv.SetGitAuth(false, "env-token", "env-user")
	srv.SetSecretBox(newTestBox(t))

	proj := gitProject()
	proj.GitURL = "file:///tmp/repo" // HostOf() → ""
	opts, err := srv.resolveGitOptions(context.Background(), proj, "/data")
	if err != nil {
		t.Fatalf("resolveGitOptions() error = %v", err)
	}
	if cs.gotHost != "" {
		t.Errorf("host lookup ran with %q, want none", cs.gotHost)
	}
	if opts.Token != "env-token" {
		t.Errorf("token = %q, want the env fallback", opts.Token)
	}
}

func TestResolveGitOptionsDecryptErrorIsCurated(t *testing.T) {
	t.Parallel()
	sealBox, openBox := newTestBox(t), newTestBox(t) // different keys
	const plain = "super-secret-token"
	cs := &credStore{
		fakeStore: &fakeStore{},
		projCred: &store.GitCredential{
			ID: 3, Kind: "https", Username: "bot",
			SecretEnc: sealSecret(t, sealBox, plain),
		},
	}
	srv := New(cs, fakeEmbedder{}, nil)
	srv.SetSecretBox(openBox)

	_, err := srv.resolveGitOptions(context.Background(), gitProject(), "/data")
	if err == nil {
		t.Fatal("resolveGitOptions() = nil error, want decrypt failure")
	}
	if !strings.Contains(err.Error(), "could not be decrypted") {
		t.Errorf("error %q lacks the curated decrypt message", err)
	}
	if strings.Contains(err.Error(), plain) {
		t.Errorf("error %q leaks the secret", err)
	}
}

func TestResolveGitOptionsSecretboxDisabled(t *testing.T) {
	t.Parallel()
	box := newTestBox(t)
	cs := &credStore{
		fakeStore: &fakeStore{},
		projCred: &store.GitCredential{
			ID: 4, Kind: "https", SecretEnc: sealSecret(t, box, "tok"),
		},
	}
	srv := New(cs, fakeEmbedder{}, nil) // SetSecretBox never called (nil box)

	_, err := srv.resolveGitOptions(context.Background(), gitProject(), "/data")
	if err == nil || !strings.Contains(err.Error(), "SEMIDX_SECRET_KEY is not set") {
		t.Fatalf("error = %v, want the missing-SEMIDX_SECRET_KEY message", err)
	}
}

func TestResolveGitOptionsLookupErrorsAreCurated(t *testing.T) {
	t.Parallel()
	for name, cs := range map[string]*credStore{
		"project scope": {fakeStore: &fakeStore{}, projErr: errors.New("pg: dsn=postgres://u:pw@db boom")},
		"host scope":    {fakeStore: &fakeStore{}, hostErr: errors.New("pg: dsn=postgres://u:pw@db boom")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := New(cs, fakeEmbedder{}, nil)
			srv.SetSecretBox(newTestBox(t))
			_, err := srv.resolveGitOptions(context.Background(), gitProject(), "/data")
			if err == nil || err.Error() != errCredentialLookup {
				t.Fatalf("error = %v, want %q", err, errCredentialLookup)
			}
		})
	}
}

func TestRunJobGitCredentialFailureFailsJobWithCuratedMessage(t *testing.T) {
	t.Parallel()
	sealBox, openBox := newTestBox(t), newTestBox(t)
	const plain = "super-secret-token"
	fs := &fakeStore{
		claimJob: &store.Job{ID: 21, Type: "full", ProjectID: 1},
		projByID: gitProject(),
	}
	cs := &credStore{
		fakeStore: fs,
		projCred: &store.GitCredential{
			ID: 5, Kind: "https", SecretEnc: sealSecret(t, sealBox, plain),
		},
	}
	srv := New(cs, fakeEmbedder{}, nil)
	srv.SetSecretBox(openBox)

	if !srv.claimAndRun(context.Background(), t.TempDir()) {
		t.Fatal("expected job to be claimed")
	}
	if !fs.failCalled {
		t.Fatal("expected FailJob")
	}
	if !strings.Contains(fs.failMsg, "could not be decrypted") {
		t.Errorf("FailJob msg %q lacks the curated decrypt message", fs.failMsg)
	}
	if strings.Contains(fs.failMsg, plain) {
		t.Errorf("FailJob msg %q leaks the secret", fs.failMsg)
	}
}

// Project-commit tracking used by the indexer's git-diff fast path; the fake
// never has a stored SHA, so indexing falls back to the full walk.
func (f *fakeStore) GetProjectCommit(context.Context, int) (string, error)  { return "", nil }
func (f *fakeStore) UpdateProjectCommit(context.Context, int, string) error { return nil }

func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// Isolate from any global/system git config (e.g. a core.hooksPath that
	// would run unrelated commit hooks on the developer's machine).
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestRunJobGitProjectClonesAndIndexes drives the full git-job path end-to-end
// with the options produced by resolveGitOptions (env fallback, no stored
// credential): clone a local file:// repo, then index it.
func TestRunJobGitProjectClonesAndIndexes(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	src := t.TempDir()
	runGitCmd(t, src, "init", "-q")
	runGitCmd(t, src, "config", "user.email", "t@example.com")
	runGitCmd(t, src, "config", "user.name", "tester")
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte("package main\nfunc main(){}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitCmd(t, src, "add", ".")
	runGitCmd(t, src, "commit", "-q", "-m", "first")

	fs := &fakeStore{
		claimJob: &store.Job{ID: 22, Type: "full", ProjectID: 1},
		projByID: &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "git", GitURL: "file://" + src},
	}
	srv := New(fs, fakeEmbedder{}, nil)
	srv.SetGitAllowFile(true)

	if !srv.claimAndRun(context.Background(), t.TempDir()) {
		t.Fatal("expected job to be claimed")
	}
	if fs.failCalled {
		t.Fatalf("job failed: %s", fs.failMsg)
	}
	if !fs.compCalled || fs.compFiles < 1 {
		t.Fatalf("CompleteJob files=%d called=%v", fs.compFiles, fs.compCalled)
	}
}
