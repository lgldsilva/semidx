package gitsync

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// testSSHKey generates an ed25519 private key PEM at runtime. Never commit a
// key fixture — gitleaks (pre-commit and CI) rejects PEM blocks in the tree.
func testSSHKey(t *testing.T) []byte {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// assertTempEmpty checks that dataDir/ssh/tmp exists but holds no leftover
// credential files.
func assertTempEmpty(t *testing.T, dataDir string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dataDir, "ssh", "tmp"))
	if err != nil {
		t.Fatalf("ssh temp dir should exist after a sync: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 0 {
		t.Errorf("ephemeral credential files leaked: %v", names)
	}
}

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"https://github.com/acme/repo.git":         "github.com",
		"https://gitea.lan:3000/o/r.git":           "gitea.lan",
		"https://user:pass@gitea.lan:3000/o/r.git": "gitea.lan",
		"https://host":          "host",
		"ssh://git@host:2222/x": "host",
		"ssh://host/x":          "host",
		"git@host:o/r.git":      "host",
		"git@gitea.raspberrypi.lan:lgldsilva/x.git": "gitea.raspberrypi.lan",
		"git@:path":        "", // empty host
		"git@ho/st:path":   "", // '/' before ':' → path, not scp
		"ftp://x/y":        "",
		"file:///srv/repo": "",
		"/local/path":      "",
		"lixo":             "",
		"":                 "",
	}
	for in, want := range cases {
		if got := HostOf(in); got != want {
			t.Errorf("HostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestHostOfPropertyNoSeparators: whatever the input, the result is a bare
// hostname — it never contains '/', '@' or ':'.
func TestHostOfPropertyNoSeparators(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		prefix := rapid.SampledFrom([]string{"", "https://", "ssh://", "git@"}).Draw(rt, "prefix")
		rest := rapid.String().Draw(rt, "rest")
		got := HostOf(prefix + rest)
		if strings.ContainsAny(got, "/@:") {
			rt.Fatalf("HostOf(%q) = %q contains a separator", prefix+rest, got)
		}
	})
}

// TestHostOfPropertyHTTPS: a well-formed https URL always yields its host,
// whatever the userinfo, port or path around it.
func TestHostOfPropertyHTTPS(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		host := rapid.StringMatching(`[a-z0-9][a-z0-9.-]{0,30}[a-z0-9]`).Draw(rt, "host")
		userinfo := rapid.SampledFrom([]string{"", "git@", "u:pw@"}).Draw(rt, "userinfo")
		tail := rapid.SampledFrom([]string{"", ":3000", "/o/r.git", ":22/o/r.git"}).Draw(rt, "tail")
		url := schemeHTTPS + userinfo + host + tail
		if got := HostOf(url); got != host {
			rt.Fatalf("HostOf(%q) = %q, want %q", url, got, host)
		}
	})
}

func TestSSHCommand(t *testing.T) {
	got := sshCommand("/d/ssh/tmp/key-1", "/d/ssh/tmp/kh-1", true)
	want := "ssh -i /d/ssh/tmp/key-1 -o UserKnownHostsFile=/d/ssh/tmp/kh-1" +
		" -o StrictHostKeyChecking=yes -o IdentitiesOnly=yes -o BatchMode=yes"
	if got != want {
		t.Errorf("strict sshCommand = %q, want %q", got, want)
	}
	got = sshCommand("/k", "/kh", false)
	if !strings.Contains(got, "-o StrictHostKeyChecking=accept-new") {
		t.Errorf("non-strict sshCommand should accept-new: %q", got)
	}
	if !strings.Contains(got, "-o BatchMode=yes") || !strings.Contains(got, "-o IdentitiesOnly=yes") {
		t.Errorf("sshCommand missing hardening flags: %q", got)
	}
}

func TestEffectiveURLSSHKeyDisablesRewrite(t *testing.T) {
	key := testSSHKey(t)
	// With a key, SSH URLs keep the SSH transport even when a token is set.
	for _, u := range []string{"git@host:o/r.git", "ssh://git@host:2222/o/r.git"} {
		if got := effectiveURL(Options{URL: u, Token: "t", SSHKey: key}); got != u {
			t.Errorf("effectiveURL(%q with key) = %q, want unchanged", u, got)
		}
	}
	// Without a key, a token still forces the https rewrite (compat).
	if got := effectiveURL(Options{URL: "git@host:o/r.git", Token: "t"}); got != "https://host/o/r.git" {
		t.Errorf("effectiveURL without key = %q, want https rewrite", got)
	}
	// Non-SSH URLs are never touched.
	if got := effectiveURL(Options{URL: "https://host/o/r.git", SSHKey: key, Token: "t"}); got != "https://host/o/r.git" {
		t.Errorf("https URL modified: %q", got)
	}
}

func TestSSHSetupPinnedKnownHosts(t *testing.T) {
	data := t.TempDir()
	opts := Options{DataDir: data, SSHKey: testSSHKey(t), SSHKnownHosts: "h ssh-ed25519 AAAA"}
	cmd, cleanup, err := sshSetup(opts, "git@h:o/r.git")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd, "-o StrictHostKeyChecking=yes") {
		t.Errorf("pinned known_hosts must be strict: %q", cmd)
	}
	entries, err := os.ReadDir(filepath.Join(data, "ssh", "tmp"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("want key + known_hosts in ssh/tmp, got %v (err %v)", entries, err)
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %o, want 0600", e.Name(), info.Mode().Perm())
		}
	}
	cleanup()
	assertTempEmpty(t, data)
}

func TestSSHSetupAcceptNewPersistsHostFile(t *testing.T) {
	data := t.TempDir()
	opts := Options{DataDir: data, SSHKey: testSSHKey(t)}
	cmd, cleanup, err := sshSetup(opts, "ssh://git@gitea.lan:2222/o/r.git")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd, "-o StrictHostKeyChecking=accept-new") {
		t.Errorf("no known_hosts content must mean accept-new: %q", cmd)
	}
	kh := filepath.Join(data, "ssh", "known_hosts.d", "gitea.lan")
	if !strings.Contains(cmd, "-o UserKnownHostsFile="+kh) {
		t.Errorf("per-host known_hosts file not wired: %q", cmd)
	}
	info, err := os.Stat(kh)
	if err != nil {
		t.Fatalf("persistent known_hosts not created: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("known_hosts mode = %o, want 0600", info.Mode().Perm())
	}
	cleanup()
	// The per-host pin survives cleanup; only the ephemeral key is removed.
	if _, err := os.Stat(kh); err != nil {
		t.Errorf("cleanup must not remove the persistent known_hosts: %v", err)
	}
	assertTempEmpty(t, data)
}

// TestSyncSSHKeyTempCleanupOnError: with a key, an ssh:// URL passes validation
// unrewritten, and the ephemeral key is removed even when the clone fails
// (nothing listens on port 1).
func TestSyncSSHKeyTempCleanupOnError(t *testing.T) {
	needGit(t)
	data := t.TempDir()
	_, err := SyncWithOptions(context.Background(), Options{
		DataDir: data, Name: "p", URL: "ssh://git@127.0.0.1:1/nope.git",
		SSHKey: testSSHKey(t),
	})
	if err == nil {
		t.Fatal("clone from a refused ssh endpoint must fail")
	}
	if strings.Contains(err.Error(), "unsupported git url") {
		t.Fatalf("ssh:// with a key must not be rewritten or rejected, got: %v", err)
	}
	assertTempEmpty(t, data)
}

// TestSyncSSHKeyTempCleanupOnSuccess: file:// transport ignores GIT_SSH_COMMAND,
// so the sync succeeds while still exercising the key file lifecycle.
func TestSyncSSHKeyTempCleanupOnSuccess(t *testing.T) {
	needGit(t)
	src := initSource(t)
	data := t.TempDir()
	path, err := SyncWithOptions(context.Background(), Options{
		DataDir: data, Name: "p", URL: "file://" + src, AllowFileURL: true,
		SSHKey: testSSHKey(t),
	})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, "a.txt")); err != nil {
		t.Errorf("clone incomplete: %v", err)
	}
	assertTempEmpty(t, data)
}

// TestSyncTokenViaEnv: git parses GIT_CONFIG_COUNT/KEY_0/VALUE_0 on every
// command and aborts on malformed entries, so a successful clone proves the
// header travels via env — argv purity is covered by TestGitConfigArgs.
func TestSyncTokenViaEnv(t *testing.T) {
	needGit(t)
	src := initSource(t)
	data := t.TempDir()
	if _, err := SyncWithOptions(context.Background(), Options{
		DataDir: data, Name: "p", URL: "file://" + src, AllowFileURL: true,
		Token: "sekret", TokenUser: "alice",
	}); err != nil {
		t.Fatalf("Sync with env-injected token: %v", err)
	}
}

// TestSyncCACertTempCleanup: the CA bundle is written to an ephemeral file,
// passed as http.sslCAInfo (ignored by file:// transport) and removed after.
func TestSyncCACertTempCleanup(t *testing.T) {
	needGit(t)
	src := initSource(t)
	data := t.TempDir()
	ca := []byte("-----BEGIN CERTIFICATE-----\nbm90IGEgcmVhbCBjZXJ0\n-----END CERTIFICATE-----\n")
	if _, err := SyncWithOptions(context.Background(), Options{
		DataDir: data, Name: "p", URL: "file://" + src, AllowFileURL: true,
		CACert: ca,
	}); err != nil {
		t.Fatalf("Sync with CACert: %v", err)
	}
	assertTempEmpty(t, data)
}

func TestSweepTempKeys(t *testing.T) {
	data := t.TempDir()
	tmp := filepath.Join(data, "ssh", "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "key-leftover"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	khd := filepath.Join(data, "ssh", "known_hosts.d")
	if err := os.MkdirAll(khd, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(khd, "gitea.lan"), []byte("pin"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SweepTempKeys(data); err != nil {
		t.Fatalf("SweepTempKeys: %v", err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("ssh/tmp should be gone, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(khd, "gitea.lan")); err != nil {
		t.Errorf("sweep must not touch known_hosts.d: %v", err)
	}
	// Sweeping a data dir that never had temp keys is a no-op.
	if err := SweepTempKeys(t.TempDir()); err != nil {
		t.Errorf("sweep of empty data dir: %v", err)
	}
}

func TestSweepTempKeys_PreservesPersistentState(t *testing.T) {
	// Property-ish: regardless of leftover file names under ssh/tmp, sweep removes
	// only that directory and never known_hosts.d entries.
	rapid.Check(t, func(rt *rapid.T) {
		data, err := os.MkdirTemp("", "sweep-*")
		if err != nil {
			rt.Fatalf("%v", err)
		}
		defer func() { _ = os.RemoveAll(data) }()
		tmp := filepath.Join(data, "ssh", "tmp")
		if err := os.MkdirAll(tmp, 0o700); err != nil {
			rt.Fatalf("%v", err)
		}
		n := rapid.IntRange(0, 5).Draw(rt, "n")
		for i := 0; i < n; i++ {
			name := rapid.StringMatching(`[a-z0-9._-]{1,12}`).Draw(rt, "name")
			_ = os.WriteFile(filepath.Join(tmp, name), []byte("x"), 0o600)
		}
		khd := filepath.Join(data, "ssh", "known_hosts.d")
		if err := os.MkdirAll(khd, 0o700); err != nil {
			rt.Fatalf("%v", err)
		}
		host := rapid.StringMatching(`[a-z][a-z0-9.-]{0,20}`).Draw(rt, "host")
		if err := os.WriteFile(filepath.Join(khd, host), []byte("pin"), 0o600); err != nil {
			rt.Fatalf("%v", err)
		}
		if err := SweepTempKeys(data); err != nil {
			rt.Fatalf("%v", err)
		}
		if _, err := os.Stat(tmp); !os.IsNotExist(err) {
			rt.Fatalf("tmp still present: %v", err)
		}
		if _, err := os.Stat(filepath.Join(khd, host)); err != nil {
			rt.Fatalf("known_hosts lost: %v", err)
		}
	})
}

// brokenSSHDir returns a dataDir where dataDir/ssh is a regular file, making
// any ephemeral credential write under ssh/tmp fail.
func brokenSSHDir(t *testing.T) string {
	t.Helper()
	data := t.TempDir()
	if err := os.WriteFile(filepath.Join(data, "ssh"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSyncSSHSetupFailure(t *testing.T) {
	if _, err := SyncWithOptions(context.Background(), Options{
		DataDir: brokenSSHDir(t), Name: "p", URL: "git@host:o/r.git", SSHKey: testSSHKey(t),
	}); err == nil {
		t.Fatal("sshSetup must fail when dataDir/ssh is a regular file")
	}
}

func TestSyncCACertWriteFailure(t *testing.T) {
	if _, err := SyncWithOptions(context.Background(), Options{
		DataDir: brokenSSHDir(t), Name: "p", URL: "https://host/o/r.git", CACert: []byte("pem"),
	}); err == nil {
		t.Fatal("writeCACert must fail when dataDir/ssh is a regular file")
	}
}

func TestSSHSetupPersistentKnownHostsFailure(t *testing.T) {
	data := t.TempDir()
	// known_hosts.d exists as a regular file → persistentKnownHosts fails after
	// the key was written; the internal cleanup must reclaim the ephemeral key.
	if err := os.MkdirAll(filepath.Join(data, "ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "ssh", "known_hosts.d"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sshSetup(Options{DataDir: data, SSHKey: testSSHKey(t)}, "git@h:o/r.git"); err == nil {
		t.Fatal("expected persistentKnownHosts failure")
	}
	assertTempEmpty(t, data)
}

func TestWriteEphemeralMkdirFailure(t *testing.T) {
	if _, err := writeEphemeral(brokenSSHDir(t), "key-*", []byte("k")); err == nil {
		t.Fatal("expected MkdirAll failure when dataDir/ssh is a regular file")
	}
}

// TestEffectiveURLNoTokenNoKey pins the credential-free path: the rewrite then
// depends only on whether an ssh client exists on the host.
func TestEffectiveURLNoTokenNoKey(t *testing.T) {
	u := "git@host:o/r.git"
	got := effectiveURL(Options{URL: u})
	if hasSSHClient() {
		if got != u {
			t.Errorf("ssh client present: URL must stay ssh, got %q", got)
		}
	} else if got != "https://host/o/r.git" {
		t.Errorf("no ssh client: URL must be rewritten to https, got %q", got)
	}
}

func TestRunGitNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := run(context.Background(), "", nil, "version")
	if err == nil || !strings.Contains(err.Error(), "git not found") {
		t.Errorf("err = %v, want git not found", err)
	}
}
