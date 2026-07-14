package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/lgldsilva/semidx/internal/gitsync"
	"github.com/lgldsilva/semidx/internal/secretbox"
	"github.com/lgldsilva/semidx/internal/store"
)

// errCredentialLookup is the CURATED message stored on a job when the
// credential lookup itself errors (as opposed to "no credential found", which
// silently degrades to the env fallback). The raw store error goes to the
// server log only — it may name hosts or carry driver internals.
// #nosec G101 -- curated user-facing error text, not a hardcoded credential.
const errCredentialLookup = "git credential lookup failed — see server logs"

// SetSecretBox wires the AES-256-GCM vault (SEMIDX_SECRET_KEY) used to decrypt
// stored git credentials. A nil box is valid: stored credentials then fail
// with a clear "SEMIDX_SECRET_KEY not set" job error, while projects without
// stored credentials keep working through the env-global fallback.
func (s *Server) SetSecretBox(box *secretbox.Box) { s.secrets = box }

// resolveGitOptions builds the gitsync.Options for syncing a git project,
// resolving credentials in precedence order:
//
//  1. the project-scoped credential in the store,
//  2. the host-scoped credential for the repo URL's host (gitsync.HostOf),
//  3. the server's env-global HTTPS token (SEMIDX_GIT_TOKEN / SEMIDX_GIT_USER).
//
// Stores that do not implement store.GitCredentialStore (e.g. SQLite) degrade
// straight to (3). Every returned error is CURATED — safe to persist as the
// job's failure message: it never contains secret material or raw store/crypto
// errors (those go to the server log).
func (s *Server) resolveGitOptions(ctx context.Context, proj *store.Project, dataDir string) (gitsync.Options, error) {
	opts := gitsync.Options{
		DataDir:      dataDir,
		Name:         proj.Name,
		URL:          proj.GitURL,
		Branch:       proj.Branch,
		AllowFileURL: s.gitAllowFile,
		SSLNoVerify:  s.gitSSLNoVerify,
	}

	cred, err := s.lookupGitCredential(ctx, proj)
	if err != nil {
		return gitsync.Options{}, err
	}
	if cred == nil {
		opts.Token = s.gitToken
		opts.TokenUser = s.gitUser
		return opts, nil
	}

	secret, err := s.openCredentialSecret(cred)
	if err != nil {
		return gitsync.Options{}, err
	}
	applyGitCredential(&opts, cred, secret)
	return opts, nil
}

// lookupGitCredential returns the stored credential for proj — project scope
// first, then host scope — or nil when none is stored or the store has no
// credential support: both degrade to the env-global fallback.
func (s *Server) lookupGitCredential(ctx context.Context, proj *store.Project) (*store.GitCredential, error) {
	gcs, ok := s.store.(store.GitCredentialStore)
	if !ok {
		return nil, nil
	}

	cred, err := gcs.GetGitCredentialForProject(ctx, proj.ID)
	if err == nil {
		return cred, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		s.log.Error("git credential lookup (project scope)", "project", proj.Name, "err", err)
		return nil, errors.New(errCredentialLookup)
	}

	host := gitsync.HostOf(proj.GitURL)
	if host == "" {
		return nil, nil
	}
	cred, err = gcs.GetGitCredentialForHost(ctx, host)
	if err == nil {
		return cred, nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	s.log.Error("git credential lookup (host scope)", "project", proj.Name, "host", host, "err", err)
	return nil, errors.New(errCredentialLookup)
}

// openCredentialSecret decrypts the credential's sealed secret. Errors are
// CURATED: they identify the credential, never its content — the raw crypto
// error stays in the server log.
func (s *Server) openCredentialSecret(cred *store.GitCredential) ([]byte, error) {
	if !s.secrets.Enabled() {
		return nil, fmt.Errorf(
			"git credential %d is configured but SEMIDX_SECRET_KEY is not set on the server", cred.ID)
	}
	secret, err := s.secrets.Open(cred.SecretEnc)
	if err != nil {
		s.log.Error("git credential decrypt failed",
			"credential", cred.ID, "key_version", cred.KeyVersion, "err", err)
		return nil, fmt.Errorf(
			"git credential %d could not be decrypted (was SEMIDX_SECRET_KEY changed?) — re-save the credential", cred.ID)
	}
	return secret, nil
}

// applyGitCredential maps a decrypted credential onto the sync options: an ssh
// credential carries a private key (plus optional pinned known_hosts), an
// https one a token/password with its basic-auth user.
func applyGitCredential(opts *gitsync.Options, cred *store.GitCredential, secret []byte) {
	if cred.Kind == "ssh" {
		opts.SSHKey = secret
		opts.SSHUser = cred.Username
		opts.SSHKnownHosts = cred.SSHKnownHosts
		return
	}
	opts.Token = string(secret)
	opts.TokenUser = cred.Username
}
