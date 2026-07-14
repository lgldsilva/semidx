package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/gitsync"
	"github.com/lgldsilva/semidx/pkg/client"
)

const (
	// Help text for commands that talk to the server vault — not a secret value.
	gitCredPathDesc       = "Requires semidx login with an admin-scoped API key" // #nosec G101 -- operator help text, not a credential
	errRepoCredNeedsLogin = "repo cred requires a server: run `semidx login` first"
	errListGitCredsFmt    = "list git credentials: %w"
)

func newRepoCredCmd(d *deps) *cobra.Command {
	c := &cobra.Command{
		Use:   "cred",
		Short: "Manage git clone credentials on the server",
		Long: `Manage git credentials stored on the server for private clone/pull.

` + gitCredPathDesc + `. Secrets are sealed server-side; list output never
includes plaintext tokens or private keys.`,
		Example: `  semidx repo cred list
  semidx repo cred set --host github.com --git-token -
  semidx repo cred set --project my-repo --ssh-key ~/.ssh/id_ed25519
  semidx repo cred rm 3`,
	}
	c.AddCommand(newRepoCredSetCmd(d))
	c.AddCommand(newRepoCredListCmd(d))
	c.AddCommand(newRepoCredRmCmd(d))
	return c
}

func newRepoCredSetCmd(d *deps) *cobra.Command {
	var host, project, kind, gitUser, gitToken, sshKey, sshKnownHosts, label string
	c := &cobra.Command{
		Use:   "set",
		Short: "Create or update a git credential",
		Long: `Create or update a git credential scoped to exactly one of --host or
--project. HTTPS credentials use --git-user and --git-token (- reads stdin).
SSH credentials use --ssh-key (file path or -) and optional --ssh-known-hosts.`,
		Example: `  semidx repo cred set --host gitea.lan --git-user deploy --git-token -
  semidx repo cred set --project private-app --ssh-key ~/.ssh/deploy_ed25519
  semidx repo cred set --host github.com --kind https --git-token "$TOKEN" --label ci`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRepoCredSet(cmd.Context(), d, repoCredFlags{
				host: host, project: project, kind: kind, gitUser: gitUser,
				gitToken: gitToken, sshKey: sshKey, sshKnownHosts: sshKnownHosts, label: label,
			})
		},
	}
	c.Flags().StringVar(&host, "host", "", "Host-scoped credential (e.g. github.com)")
	c.Flags().StringVar(&project, "project", "", "Project-scoped credential (name or numeric id)")
	c.Flags().StringVar(&kind, "kind", "", "Credential kind: https or ssh (inferred from flags when omitted)")
	c.Flags().StringVar(&gitUser, "git-user", "", "HTTPS username (optional)")
	c.Flags().StringVar(&gitToken, "git-token", "", "HTTPS token/password (use - to read from stdin)")
	c.Flags().StringVar(&sshKey, "ssh-key", "", "SSH private key file (or - for stdin)")
	c.Flags().StringVar(&sshKnownHosts, "ssh-known-hosts", "", "SSH known_hosts file (or - for stdin)")
	c.Flags().StringVar(&label, "label", "", "Optional label")
	return c
}

func newRepoCredListCmd(d *deps) *cobra.Command {
	c := &cobra.Command{
		Use:     "list",
		Short:   "List stored git credentials",
		Example: "  semidx repo cred list",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRepoCredList(cmd.Context(), d)
		},
	}
	return c
}

func newRepoCredRmCmd(d *deps) *cobra.Command {
	c := &cobra.Command{
		Use:     "rm <id>",
		Short:   "Delete a git credential by id",
		Example: "  semidx repo cred rm 3",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.Atoi(args[0])
			if err != nil || id <= 0 {
				return fmt.Errorf("invalid credential id %q", args[0])
			}
			return runRepoCredRm(cmd.Context(), d, id)
		},
	}
	return c
}

type repoCredFlags struct {
	host, project, kind, gitUser, gitToken, sshKey, sshKnownHosts, label string
	hostCredential                                                       bool
}

func (f repoCredFlags) hasSecret() bool {
	return strings.TrimSpace(f.gitToken) != "" || strings.TrimSpace(f.sshKey) != ""
}

func runRepoCredSet(ctx context.Context, d *deps, f repoCredFlags) error {
	if !d.remote() {
		return fmt.Errorf("%s", errRepoCredNeedsLogin)
	}
	cli := d.apiClient()
	in, err := buildGitCredentialInput(f)
	if err != nil {
		return err
	}
	if strings.TrimSpace(f.project) != "" {
		pid, err := resolveGitCredentialProjectID(ctx, cli, strings.TrimSpace(f.project))
		if err != nil {
			return err
		}
		in.ProjectID = &pid
	}
	existing, err := findGitCredentialForScope(ctx, cli, in.ProjectID, in.Host)
	if err != nil {
		return err
	}
	if existing != nil {
		updated, err := cli.UpdateGitCredential(ctx, existing.ID, client.GitCredentialUpdateInput{
			Kind: in.Kind, Username: in.Username, Secret: in.Secret,
			Label: in.Label, SSHKnownHosts: in.SSHKnownHosts,
		})
		if err != nil {
			return fmt.Errorf("update git credential: %w", err)
		}
		printGitCredential("Updated", updated)
		return nil
	}
	created, err := cli.CreateGitCredential(ctx, in)
	if err != nil {
		return fmt.Errorf("create git credential: %w", err)
	}
	printGitCredential("Created", created)
	return nil
}

func runRepoCredList(ctx context.Context, d *deps) error {
	if !d.remote() {
		return fmt.Errorf("%s", errRepoCredNeedsLogin)
	}
	creds, err := d.apiClient().ListGitCredentials(ctx)
	if err != nil {
		return fmt.Errorf(errListGitCredsFmt, err)
	}
	if len(creds) == 0 {
		fmt.Println("No git credentials stored.")
		return nil
	}
	fmt.Printf("%-4s %-8s %-28s %-5s %-12s %s\n", "ID", "SCOPE", "TARGET", "KIND", "USER", "FINGERPRINT")
	for _, c := range creds {
		target := formatCredTarget(c)
		user := c.Username
		if user == "" {
			user = "—"
		}
		fp := c.SSHFingerprint
		if fp == "" {
			fp = "—"
		}
		fmt.Printf("%-4d %-8s %-28s %-5s %-12s %s\n", c.ID, c.Scope, target, c.Kind, user, fp)
	}
	return nil
}

func runRepoCredRm(ctx context.Context, d *deps, id int) error {
	if !d.remote() {
		return fmt.Errorf("%s", errRepoCredNeedsLogin)
	}
	if err := d.apiClient().DeleteGitCredential(ctx, id); err != nil {
		return fmt.Errorf("delete git credential: %w", err)
	}
	fmt.Printf("Deleted git credential #%d.\n", id)
	return nil
}

func applyRepoAddCredential(ctx context.Context, cli *client.Client, gitURL string, f repoCredFlags) (*client.ProjectCredentialInput, error) {
	if !f.hasSecret() {
		return nil, nil
	}
	mat, err := parseGitCredMaterial(f)
	if err != nil {
		return nil, err
	}
	if f.hostCredential {
		host := gitsync.HostOf(gitURL)
		if host == "" {
			return nil, fmt.Errorf("could not derive git host from %q for --host-credential", gitURL)
		}
		if _, err := cli.CreateGitCredential(ctx, client.GitCredentialInput{
			Host: host, Kind: mat.kind, Username: mat.username, Secret: mat.secret,
			Label: mat.label, SSHKnownHosts: mat.sshKnownHosts,
		}); err != nil {
			return nil, fmt.Errorf("create host git credential: %w", err)
		}
		fmt.Printf("Stored host-scoped git credential for %s\n", host)
		return nil, nil
	}
	return &client.ProjectCredentialInput{
		Kind: mat.kind, Username: mat.username, Secret: mat.secret,
		Label: mat.label, SSHKnownHosts: mat.sshKnownHosts,
	}, nil
}

type gitCredMaterial struct {
	kind, username, secret, label, sshKnownHosts string
}

func parseGitCredMaterial(f repoCredFlags) (gitCredMaterial, error) {
	if !f.hasSecret() {
		return gitCredMaterial{}, fmt.Errorf("a secret is required: use --git-token or --ssh-key")
	}
	if strings.TrimSpace(f.gitToken) != "" && strings.TrimSpace(f.sshKey) != "" {
		return gitCredMaterial{}, fmt.Errorf("use either --git-token or --ssh-key, not both")
	}
	if strings.TrimSpace(f.gitToken) != "" {
		return parseHTTPSGitCredMaterial(f)
	}
	if strings.TrimSpace(f.sshKey) != "" {
		return parseSSHGitCredMaterial(f)
	}
	return gitCredMaterial{}, fmt.Errorf("a secret is required: use --git-token or --ssh-key")
}

func parseHTTPSGitCredMaterial(f repoCredFlags) (gitCredMaterial, error) {
	kind := strings.TrimSpace(f.kind)
	if kind == "" {
		kind = "https"
	}
	if kind != "https" {
		return gitCredMaterial{}, fmt.Errorf("kind must be https when using --git-token")
	}
	secret, err := readCredentialMaterial(f.gitToken)
	if err != nil {
		return gitCredMaterial{}, err
	}
	if strings.TrimSpace(secret) == "" {
		return gitCredMaterial{}, fmt.Errorf("credential secret must not be empty")
	}
	return gitCredMaterial{
		kind: kind, username: strings.TrimSpace(f.gitUser), secret: secret,
		label: strings.TrimSpace(f.label),
	}, nil
}

func parseSSHGitCredMaterial(f repoCredFlags) (gitCredMaterial, error) {
	kind := strings.TrimSpace(f.kind)
	if kind == "" {
		kind = "ssh"
	}
	if kind != "ssh" {
		return gitCredMaterial{}, fmt.Errorf("kind must be ssh when using --ssh-key")
	}
	secret, err := readCredentialMaterial(f.sshKey)
	if err != nil {
		return gitCredMaterial{}, err
	}
	var knownHosts string
	if strings.TrimSpace(f.sshKnownHosts) != "" {
		knownHosts, err = readCredentialMaterial(f.sshKnownHosts)
		if err != nil {
			return gitCredMaterial{}, err
		}
	}
	if strings.TrimSpace(secret) == "" {
		return gitCredMaterial{}, fmt.Errorf("credential secret must not be empty")
	}
	return gitCredMaterial{
		kind: kind, username: strings.TrimSpace(f.gitUser), secret: secret,
		label: strings.TrimSpace(f.label), sshKnownHosts: knownHosts,
	}, nil
}

func buildGitCredentialInput(f repoCredFlags) (client.GitCredentialInput, error) {
	hasHost := strings.TrimSpace(f.host) != ""
	hasProject := strings.TrimSpace(f.project) != ""
	if hasHost == hasProject {
		return client.GitCredentialInput{}, fmt.Errorf("set exactly one of --host or --project")
	}
	mat, err := parseGitCredMaterial(f)
	if err != nil {
		return client.GitCredentialInput{}, err
	}
	in := client.GitCredentialInput{
		Kind: mat.kind, Username: mat.username, Secret: mat.secret,
		Label: mat.label, SSHKnownHosts: mat.sshKnownHosts,
	}
	if hasHost {
		in.Host = strings.TrimSpace(f.host)
	}
	return in, nil
}

func resolveGitCredentialProjectID(ctx context.Context, cli *client.Client, ref string) (int, error) {
	if id, err := strconv.Atoi(ref); err == nil && id > 0 {
		return id, nil
	}
	creds, err := cli.ListGitCredentials(ctx)
	if err != nil {
		return 0, fmt.Errorf(errListGitCredsFmt, err)
	}
	for _, c := range creds {
		if c.ProjectID != nil && c.ProjectName == ref {
			return *c.ProjectID, nil
		}
	}
	if _, err := cli.GetProject(ctx, ref); err != nil {
		return 0, fmt.Errorf("project %q: %w", ref, err)
	}
	return 0, fmt.Errorf("project %q has no credential yet — pass the numeric project id (admin UI) or use `semidx repo add` with --git-token/--ssh-key", ref)
}

func findGitCredentialForScope(ctx context.Context, cli *client.Client, projectID *int, host string) (*client.GitCredential, error) {
	creds, err := cli.ListGitCredentials(ctx)
	if err != nil {
		return nil, fmt.Errorf(errListGitCredsFmt, err)
	}
	for i := range creds {
		c := &creds[i]
		if projectID != nil && c.ProjectID != nil && *c.ProjectID == *projectID {
			return c, nil
		}
		if host != "" && strings.EqualFold(c.Host, host) {
			return c, nil
		}
	}
	return nil, nil
}

func printGitCredential(verb string, c *client.GitCredential) {
	fmt.Printf("%s git credential #%d (%s %s, kind=%s", verb, c.ID, c.Scope, formatCredTarget(*c), c.Kind)
	if c.SSHFingerprint != "" {
		fmt.Printf(", fingerprint=%s", c.SSHFingerprint)
	}
	fmt.Println(")")
}

func formatCredTarget(c client.GitCredential) string {
	switch c.Scope {
	case "host":
		if c.Host != "" {
			return c.Host
		}
		return "host"
	case "project":
		if c.ProjectName != "" {
			return c.ProjectName
		}
		if c.ProjectID != nil {
			return fmt.Sprintf("#%d", *c.ProjectID)
		}
		return "project"
	default:
		return c.Scope
	}
}

// readCredentialMaterial reads secret material from "-" (stdin, up to 1 MiB) or
// from a filesystem path.
func readCredentialMaterial(spec string) (string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", nil
	}
	if spec == "-" {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
		if err != nil {
			return "", fmt.Errorf("read credential from stdin: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	b, err := os.ReadFile(filepath.Clean(spec))
	if err != nil {
		return "", fmt.Errorf("read credential file %s: %w", spec, err)
	}
	return strings.TrimSpace(string(b)), nil
}
