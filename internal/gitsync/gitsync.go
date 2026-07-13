// Package gitsync clones or updates a git repository into the server's data
// directory so the server can index projects it owns, without clients uploading
// anything.
package gitsync

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lgldsilva/semidx/internal/gitenv"
)

// Options configures a server-side sync, including credentials and TLS handling
// for private/self-signed hosts (homelab Gitea, GitHub with a token).
type Options struct {
	DataDir      string
	Name         string
	URL          string
	Branch       string
	AllowFileURL bool // permit file:// URLs (SEMIDX_GIT_ALLOW_FILE)
	// SSLNoVerify disables TLS verification for the clone/pull (self-signed
	// homelab certs). Applied only to this git invocation.
	SSLNoVerify bool
	// Token authenticates HTTPS clones of private repos WITHOUT embedding the
	// secret in the URL/DB — injected as an Authorization header. TokenUser is
	// the basic-auth user (default "x-access-token", which GitHub and Gitea both
	// accept with a PAT as the password).
	Token     string
	TokenUser string
}

// Sync is the credential-free sync (backwards-compatible entry point).
func Sync(ctx context.Context, dataDir, name, url, branch string, allowFileURL bool) (string, error) {
	return SyncWithOptions(ctx, Options{
		DataDir: dataDir, Name: name, URL: url, Branch: branch, AllowFileURL: allowFileURL,
	})
}

// SyncWithOptions ensures dataDir/repos/<name> holds an up-to-date checkout of
// the repo and returns its path. It clones (shallow) on first use and
// fast-forward pulls afterwards. Only https:// and git@ (SSH) URLs are accepted
// by default; file:// only when AllowFileURL. An scp-style SSH URL is rewritten
// to https:// when a token is configured or no ssh client is present (the server
// image has none), so token/HTTPS auth can be used.
func SyncWithOptions(ctx context.Context, opts Options) (string, error) {
	url := opts.URL
	if isSSHURL(url) && (opts.Token != "" || !hasSSHClient()) {
		url = normalizeToHTTPS(url)
	}
	if !validURL(url, opts.AllowFileURL) {
		return "", fmt.Errorf("unsupported git url %q (want https:// or git@)", url)
	}
	repoPath := filepath.Join(opts.DataDir, "repos", opts.Name)
	cfg := gitConfigArgs(opts)

	if _, err := os.Stat(filepath.Join(repoPath, ".git")); err == nil {
		if err := run(ctx, repoPath, append(cfg, "pull", "--ff-only")...); err != nil {
			return "", fmt.Errorf("git pull: %w", err)
		}
		return repoPath, nil
	}

	if err := os.MkdirAll(filepath.Dir(repoPath), 0o750); err != nil {
		return "", err
	}
	args := append(cfg, "clone", "--depth", "50")
	if opts.Branch != "" {
		args = append(args, "--branch", opts.Branch)
	}
	args = append(args, url, repoPath)
	if err := run(ctx, "", args...); err != nil {
		return "", fmt.Errorf("git clone: %w", err)
	}
	return repoPath, nil
}

// gitConfigArgs builds the leading "-c key=value" flags for TLS/credentials.
func gitConfigArgs(opts Options) []string {
	var args []string
	if opts.SSLNoVerify {
		args = append(args, "-c", "http.sslVerify=false")
	}
	if opts.Token != "" {
		user := opts.TokenUser
		if user == "" {
			user = "x-access-token"
		}
		auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + opts.Token))
		args = append(args, "-c", "http.extraHeader=Authorization: Basic "+auth)
	}
	return args
}

const (
	schemeHTTPS = "https://"
	schemeSSH   = "ssh://"
)

func isSSHURL(url string) bool {
	return strings.HasPrefix(url, "git@") || strings.HasPrefix(url, schemeSSH)
}

func hasSSHClient() bool {
	_, err := exec.LookPath("ssh")
	return err == nil
}

// normalizeToHTTPS rewrites an scp-style (git@host:owner/repo.git) or ssh:// URL
// to https://host/owner/repo.git, dropping any user and port.
func normalizeToHTTPS(url string) string {
	switch {
	case strings.HasPrefix(url, "git@"):
		rest := strings.TrimPrefix(url, "git@")
		if i := strings.Index(rest, ":"); i > 0 {
			return schemeHTTPS + rest[:i] + "/" + rest[i+1:]
		}
	case strings.HasPrefix(url, schemeSSH):
		rest := strings.TrimPrefix(url, schemeSSH)
		rest = strings.TrimPrefix(rest, "git@")
		if slash := strings.Index(rest, "/"); slash > 0 {
			hostport, path := rest[:slash], rest[slash+1:]
			if c := strings.Index(hostport, ":"); c > 0 {
				hostport = hostport[:c]
			}
			return schemeHTTPS + hostport + "/" + path
		}
	}
	return url
}

func validURL(url string, allowFile bool) bool {
	if strings.HasPrefix(url, schemeHTTPS) || strings.HasPrefix(url, "git@") {
		return true
	}
	return allowFile && strings.HasPrefix(url, "file://")
}

func run(ctx context.Context, dir string, args ...string) error {
	// Verify the executable resolves to a real binary.
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git not found: %w", err)
	}
	var cmdArgs []string
	if dir != "" {
		// dir is the repo path created by Sync (filepath.Join(dataDir, "repos", name))
		// or empty for initial clone; validated indirectly via validURL above.
		cmdArgs = append([]string{"git", "-C", dir}, args...)
	} else {
		cmdArgs = append([]string{"git"}, args...)
	}
	cmd := exec.CommandContext(ctx, "git")
	cmd.Args = cmdArgs
	// Drop any inherited GIT_DIR/GIT_WORK_TREE so the command targets dir (or the
	// clone destination), not an ambient repo leaked by a hook or bare worktree.
	cmd.Env = gitenv.Clean(cmd.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
