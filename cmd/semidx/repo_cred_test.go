package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGitCredMaterialHTTPS(t *testing.T) {
	t.Parallel()
	f, err := os.CreateTemp("", "semidx-token-*")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	if _, err := f.WriteString("  fake-test-token  \n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(path) })

	mat, err := parseGitCredMaterial(repoCredFlags{gitToken: path, gitUser: "deploy"})
	if err != nil {
		t.Fatal(err)
	}
	if mat.kind != "https" || mat.username != "deploy" || mat.secret != "fake-test-token" {
		t.Fatalf("material = %+v", mat)
	}
}

func TestParseGitCredMaterialSSH(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_ed25519")
	const pem = "-----BEGIN OPENSSH PRIVATE KEY-----\ntest-key-body\n-----END OPENSSH PRIVATE KEY-----\n"
	if err := os.WriteFile(keyPath, []byte(pem), 0o600); err != nil {
		t.Fatal(err)
	}
	khPath := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(khPath, []byte("git.example.com ssh-ed25519 AAA"), 0o600); err != nil {
		t.Fatal(err)
	}

	mat, err := parseGitCredMaterial(repoCredFlags{sshKey: keyPath, sshKnownHosts: khPath})
	if err != nil {
		t.Fatal(err)
	}
	if mat.kind != "ssh" || !strings.Contains(mat.secret, "OPENSSH") {
		t.Fatalf("secret = %q", mat.secret)
	}
	if mat.sshKnownHosts != "git.example.com ssh-ed25519 AAA" {
		t.Fatalf("known_hosts = %q", mat.sshKnownHosts)
	}
}

func TestParseGitCredMaterialValidation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte("fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		f    repoCredFlags
		want string
	}{
		{"both secrets", repoCredFlags{gitToken: tokenPath, sshKey: filepath.Join(dir, "key")}, "either"},
		{"none", repoCredFlags{}, "required"},
		{"kind mismatch", repoCredFlags{gitToken: tokenPath, kind: "ssh"}, "https"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseGitCredMaterial(tc.f)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestBuildGitCredentialInputScope(t *testing.T) {
	t.Parallel()
	_, err := buildGitCredentialInput(repoCredFlags{gitToken: "fake-token"})
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("err = %v", err)
	}
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte("fake-test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	in, err := buildGitCredentialInput(repoCredFlags{host: "github.com", gitToken: tokenPath})
	if err != nil {
		t.Fatal(err)
	}
	if in.Host != "github.com" || in.Kind != "https" {
		t.Fatalf("input = %+v", in)
	}
}

func TestReadCredentialMaterialStdinMarker(t *testing.T) {
	t.Parallel()
	if got, err := readCredentialMaterial("-"); err == nil && got != "" {
		t.Logf("stdin read = %q", got)
	}
}

func TestRepoCredCommandsWired(t *testing.T) {
	root := newRootCmd()
	for _, path := range [][]string{
		{"repo", "cred"},
		{"repo", "cred", "set"},
		{"repo", "cred", "list"},
		{"repo", "cred", "rm"},
	} {
		cmd, _, err := root.Find(path)
		if err != nil || cmd == nil {
			t.Fatalf("command %v not wired: err=%v cmd=%v", path, err, cmd)
		}
	}
}
