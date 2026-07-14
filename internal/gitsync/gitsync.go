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
	// CACert is a PEM CA bundle used to verify HTTPS remotes signed by a
	// private CA without disabling verification. It is written to an ephemeral
	// 0600 file and passed as http.sslCAInfo for this invocation only.
	CACert []byte
	// Token authenticates HTTPS clones of private repos WITHOUT embedding the
	// secret in the URL/DB — injected as an Authorization header through
	// GIT_CONFIG_* environment variables (git ≥2.31) so it never shows up in
	// the process argv (`ps`). TokenUser is the basic-auth user (default
	// "x-access-token", which GitHub and Gitea both accept with a PAT as the
	// password).
	Token     string
	TokenUser string
	// SSHKey is a PEM-encoded private key. Its presence switches the sync to
	// SSH transport: ssh URLs are NOT rewritten to https and git runs with a
	// GIT_SSH_COMMAND pointing at an ephemeral 0600 copy of the key, removed
	// when the sync returns (SweepTempKeys reclaims leftovers after a crash).
	SSHKey []byte
	// SSHUser is informative only — the ssh user comes from the URL itself
	// (ssh://user@host/… or the scp-like git@host:…). Defaults to "git".
	SSHUser string
	// SSHKnownHosts is optional known_hosts content. When set, the host key is
	// pinned for this sync (StrictHostKeyChecking=yes). When empty, first
	// contact records the host key under dataDir/ssh/known_hosts.d/<host>
	// (StrictHostKeyChecking=accept-new), which pins subsequent syncs.
	SSHKnownHosts string
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
// by default; file:// only when AllowFileURL. An SSH URL is rewritten to
// https:// when a token is configured or no ssh client is present (the server
// image has none) — unless an SSHKey is provided, which keeps the SSH transport
// (ssh:// URLs are then accepted as-is, e.g. for non-standard ports).
func SyncWithOptions(ctx context.Context, opts Options) (string, error) {
	url := effectiveURL(opts)
	sshMode := len(opts.SSHKey) > 0
	sshURLAllowed := sshMode && isSSHURL(url) // key present → ssh:// accepted as-is
	if !validURL(url, opts.AllowFileURL) && !sshURLAllowed {
		return "", fmt.Errorf("unsupported git url %q (want https:// or git@)", url)
	}

	extraEnv := gitConfigEnv(opts)

	caPath, caCleanup, err := writeCACert(opts)
	if err != nil {
		return "", err
	}
	defer caCleanup()

	if sshMode {
		sshCmd, sshCleanup, err := sshSetup(opts, url)
		if err != nil {
			return "", err
		}
		defer sshCleanup()
		extraEnv = append(extraEnv, "GIT_SSH_COMMAND="+sshCmd)
	}

	repoPath := filepath.Join(opts.DataDir, "repos", opts.Name)
	cfg := gitConfigArgs(opts, caPath)

	if _, err := os.Stat(filepath.Join(repoPath, ".git")); err == nil {
		if err := run(ctx, repoPath, extraEnv, append(cfg, "pull", "--ff-only")...); err != nil {
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
	if err := run(ctx, "", extraEnv, args...); err != nil {
		return "", fmt.Errorf("git clone: %w", err)
	}
	return repoPath, nil
}

// effectiveURL applies the ssh→https rewrite policy: an SSH URL is rewritten
// when token auth is configured or no ssh client is available — unless an SSH
// key is provided, which keeps the SSH transport.
func effectiveURL(opts Options) string {
	if isSSHURL(opts.URL) && len(opts.SSHKey) == 0 && (opts.Token != "" || !hasSSHClient()) {
		return normalizeToHTTPS(opts.URL)
	}
	return opts.URL
}

// gitConfigArgs builds the leading "-c key=value" flags for the non-secret TLS
// knobs. Credentials never travel through argv (visible in `ps`) — the
// Authorization header goes through gitConfigEnv instead.
func gitConfigArgs(opts Options, caPath string) []string {
	var args []string
	if opts.SSLNoVerify {
		args = append(args, "-c", "http.sslVerify=false")
	}
	if caPath != "" {
		args = append(args, "-c", "http.sslCAInfo="+caPath)
	}
	return args
}

// gitConfigEnv injects the Authorization header through GIT_CONFIG_COUNT /
// GIT_CONFIG_KEY_0 / GIT_CONFIG_VALUE_0 (git ≥2.31) so the secret never
// appears in the process argv.
func gitConfigEnv(opts Options) []string {
	if opts.Token == "" {
		return nil
	}
	user := opts.TokenUser
	if user == "" {
		user = "x-access-token"
	}
	auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + opts.Token))
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic " + auth,
	}
}

// writeCACert materialises opts.CACert into an ephemeral file under the data
// dir and returns its path (empty when no CA is configured) plus a cleanup.
func writeCACert(opts Options) (string, func(), error) {
	if len(opts.CACert) == 0 {
		return "", func() { /* no ephemeral file to remove */ }, nil
	}
	path, err := writeEphemeral(opts.DataDir, "ca-*.pem", opts.CACert)
	if err != nil {
		return "", nil, err
	}
	return path, func() { _ = os.Remove(path) }, nil
}

// sshSetup writes the private key (and, when pinned, the known_hosts content)
// to ephemeral files and returns the GIT_SSH_COMMAND value plus a cleanup that
// removes them. Without pinned known_hosts, the host key is persisted under
// dataDir/ssh/known_hosts.d/<host> with accept-new: first contact records the
// key, later syncs enforce it.
func sshSetup(opts Options, url string) (string, func(), error) {
	keyPath, err := writeEphemeral(opts.DataDir, "key-*", opts.SSHKey)
	if err != nil {
		return "", nil, err
	}
	ephemeral := []string{keyPath}
	cleanup := func() {
		for _, p := range ephemeral {
			_ = os.Remove(p)
		}
	}

	var khPath string
	strict := opts.SSHKnownHosts != ""
	if strict {
		khPath, err = writeEphemeral(opts.DataDir, "known_hosts-*", []byte(opts.SSHKnownHosts))
		if err != nil {
			cleanup()
			return "", nil, err
		}
		ephemeral = append(ephemeral, khPath)
	} else {
		khPath, err = persistentKnownHosts(opts.DataDir, url)
		if err != nil {
			cleanup()
			return "", nil, err
		}
	}
	return sshCommand(keyPath, khPath, strict), cleanup, nil
}

// sshCommand builds the GIT_SSH_COMMAND value. IdentitiesOnly stops agent /
// default keys from shadowing the configured one; BatchMode fails fast instead
// of hanging on a passphrase or password prompt.
func sshCommand(keyPath, khPath string, strict bool) string {
	policy := "accept-new"
	if strict {
		policy = "yes"
	}
	return "ssh -i " + keyPath +
		" -o UserKnownHostsFile=" + khPath +
		" -o StrictHostKeyChecking=" + policy +
		" -o IdentitiesOnly=yes -o BatchMode=yes"
}

// persistentKnownHosts ensures dataDir/ssh/known_hosts.d/<host> exists (0600)
// and returns its path. A per-host file keeps accept-new pinning independent
// across remotes.
func persistentKnownHosts(dataDir, url string) (string, error) {
	host := HostOf(url)
	if host == "" || host == "." || host == ".." {
		host = "default"
	}
	dir := filepath.Clean(filepath.Join(dataDir, "ssh", "known_hosts.d"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, host)
	// #nosec G304 -- path derives from the server's own DataDir plus a hostname
	// sanitised by HostOf (never contains '/', '@' or ':'), not from user input.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	return path, f.Close()
}

// tempDir returns dataDir/ssh/tmp, the directory holding ephemeral credential
// material (private keys, pinned known_hosts, CA bundles) for in-flight syncs.
func tempDir(dataDir string) string {
	return filepath.Clean(filepath.Join(dataDir, "ssh", "tmp"))
}

// writeEphemeral writes data to a uniquely-named 0600 file inside
// tempDir(dataDir), creating the directory 0700 on first use, and returns the
// file path. Callers remove the file when the sync finishes.
func writeEphemeral(dataDir, pattern string, data []byte) (string, error) {
	dir := tempDir(dataDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, pattern) // os.CreateTemp creates the file 0600
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// SweepTempKeys removes dataDir/ssh/tmp entirely — ephemeral credential files
// (private keys, pinned known_hosts, CA bundles) left behind by syncs that died
// before their deferred cleanup ran (crash, SIGKILL). The server should call it
// once at startup, before accepting jobs; that wiring lands in a follow-up PR.
// Persistent state (known_hosts.d) is untouched.
func SweepTempKeys(dataDir string) error {
	return os.RemoveAll(tempDir(dataDir))
}

// HostOf extracts the bare hostname (no user, port or path) from a git URL:
// https://host[:port]/…, ssh://[user@]host[:port]/… or the scp-like
// git@host:path. It returns "" for anything it does not recognise.
func HostOf(gitURL string) string {
	switch {
	case strings.HasPrefix(gitURL, schemeHTTPS):
		return hostFromAuthority(strings.TrimPrefix(gitURL, schemeHTTPS))
	case strings.HasPrefix(gitURL, schemeSSH):
		return hostFromAuthority(strings.TrimPrefix(gitURL, schemeSSH))
	case strings.HasPrefix(gitURL, "git@"):
		rest := strings.TrimPrefix(gitURL, "git@")
		i := strings.IndexByte(rest, ':')
		if i <= 0 {
			return ""
		}
		host := rest[:i]
		if strings.ContainsAny(host, "/@") {
			return "" // a '/' before the ':' means git reads it as a path, not scp
		}
		return host
	}
	return ""
}

// hostFromAuthority reduces "user@host:port/path" to "host": drop the path,
// then anything up to the last '@', then the port.
func hostFromAuthority(rest string) string {
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		rest = rest[:slash]
	}
	if at := strings.LastIndexByte(rest, '@'); at >= 0 {
		rest = rest[at+1:]
	}
	if c := strings.IndexByte(rest, ':'); c >= 0 {
		rest = rest[:c]
	}
	return rest
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

func run(ctx context.Context, dir string, extraEnv []string, args ...string) error {
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
	// extraEnv goes last: on duplicate keys os/exec keeps the last value, so the
	// sync's GIT_CONFIG_*/GIT_SSH_COMMAND win over anything ambient.
	cmd.Env = append(gitenv.Clean(cmd.Environ()), extraEnv...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
