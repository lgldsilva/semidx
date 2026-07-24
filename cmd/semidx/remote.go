package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/clientconfig"
	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/skills"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/pkg/client"
)

// remoteToResponse adapts an SDK search response into the internal search.Response
// so the same formatters (Human/Grep/JSON) render remote and embedded results
// identically — this is what guarantees the sgrep `file:line:content` golden holds
// in both modes. The project Path is left empty; sgrep fills it with the local cwd.
func remoteToResponse(cr *client.SearchResponse) *search.Response {
	results := make([]store.SearchResult, 0, len(cr.Results))
	for _, h := range cr.Results {
		results = append(results, store.SearchResult{
			FilePath:  h.Path,
			Content:   h.Content,
			Score:     h.Score,
			StartLine: h.StartLine,
			EndLine:   h.EndLine,
		})
	}
	return &search.Response{
		Project:    &store.Project{Name: cr.Project},
		Model:      cr.Model,
		Results:    results,
		Fallback:   cr.Fallback,
		Degraded:   cr.Degraded,
		RetryAfter: time.Duration(cr.RetryAfterMS) * time.Millisecond,
	}
}

// Search resolution now lives in searchtargets.go (runSearchTargets): a --project
// path resolves by unique identity, no --project auto-detects the enclosing
// project (or searches all), and results can span several projects.

// currentWorktreeRoot returns the current git worktree's toplevel, or "" if the
// working directory is not in a git repo. sgrep uses it to anchor result paths at
// the caller's worktree rather than the (possibly different) indexed checkout.
func currentWorktreeRoot(ctx context.Context) string {
	if gi := gitmeta.Resolve(ctx, "."); gi.IsGit {
		return gi.Toplevel
	}
	return ""
}

// newLoginCmd stores the server URL + token in the client config and verifies the
// server is reachable.
func newLoginCmd(d *deps) *cobra.Command {
	var token, tenantSlug, workspaceSlug, defaultProject string
	c := &cobra.Command{
		Use:   "login <server-url>",
		Short: "Save credentials for a semidx server and verify reachability",
		Long: `Save the URL and API token for a remote semidx server (health-checking it
first) so search/sgrep/repo/push run against that server instead of a local index.
"semidx index" still writes locally unless you pass --to-server or use push.
The token comes from --token or the SEMIDX_TOKEN environment variable.

Use "semidx logout" to forget the server, or "--local" / "--backend local" on a
single command to ignore the login for that run.`,
		Example: `  semidx login https://semidx.example.com --token "$SEMIDX_TOKEN"
  semidx login https://semidx.example.com --token abc --default-project my-repo`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if token == "" {
				token = os.Getenv("SEMIDX_TOKEN")
			}
			if token == "" {
				return fmt.Errorf("a token is required: pass --token or set SEMIDX_TOKEN")
			}
			cfg := &clientconfig.Config{ServerURL: args[0], Token: token, Tenant: tenantSlug, Workspace: workspaceSlug, DefaultProject: defaultProject}
			d.client = cfg
			d.useRemote = true
			if err := d.apiClient().Healthz(cmd.Context()); err != nil {
				return fmt.Errorf("cannot reach server at %s: %w", args[0], err)
			}
			if err := clientconfig.Save(cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			path, _ := clientconfig.Path()
			fmt.Printf("Logged in to %s (saved to %s)\n", args[0], path)
			fmt.Println("Search/status/mcp use this server. Index locally with `semidx --local index`, or push with `semidx push`.")
			return nil
		},
	}
	c.Flags().StringVar(&token, "token", "", "API token (or set SEMIDX_TOKEN)")
	c.Flags().StringVar(&tenantSlug, "tenant", "", "Tenant slug (also sent by SEMIDX_TENANT)")
	c.Flags().StringVar(&workspaceSlug, "workspace", "", "Workspace slug (also sent by SEMIDX_WORKSPACE)")
	c.Flags().StringVar(&defaultProject, "default-project", "", "Default project for search/sgrep")
	return c
}

// newLogoutCmd removes the saved server URL and token so the CLI returns to
// local (or Postgres) backend selection.
func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove saved server credentials from semidx login",
		Long: `Delete the client config file written by "semidx login" (server URL and API
token). Subsequent commands use the local SQLite/Postgres backend unless you
log in again or set SEMIDX_SERVER_URL / SEMIDX_TOKEN in the environment.`,
		Example: "  semidx logout",
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			path, err := clientconfig.Path()
			if err != nil {
				return err
			}
			if err := clientconfig.Remove(); err != nil {
				return fmt.Errorf("logout: %w", err)
			}
			fmt.Printf("Logged out (removed %s)\n", path)
			return nil
		},
	}
}

// newRepoCmd groups server-side repository management (git projects the server
// clones and indexes itself).
func newRepoCmd(d *deps) *cobra.Command {
	c := &cobra.Command{
		Use:   "repo",
		Short: "Manage git repositories indexed by the server",
		Long: `Manage git repositories the server clones and indexes itself (server-side
indexing). Requires "semidx login". See "semidx repo add".`,
		Example: "  semidx repo add https://github.com/org/project.git",
	}
	c.AddCommand(newRepoAddCmd(d))
	c.AddCommand(newRepoCredCmd(d))
	c.AddCommand(newRepoWorktreesCmd(d))
	c.AddCommand(newRepoBranchesCmd(d))
	c.AddCommand(newRepoInfoCmd(d))
	return c
}

// newSkillsCmd manages the embedded agent skills.
func newSkillsCmd(_ *deps) *cobra.Command {
	c := &cobra.Command{
		Use:   "skills",
		Short: "Install the agent skills that teach AI assistants to use semidx",
		Long: `Manage the bundled agent skills that teach AI assistants how to use semidx.
See "semidx skills install".`,
		Example: "  semidx skills install --target claude-code",
	}
	c.AddCommand(newSkillsInstallCmd())
	return c
}

func newSkillsInstallCmd() *cobra.Command {
	var target, dir string
	var force, all bool
	c := &cobra.Command{
		Use:   "install",
		Short: "Write the bundled skills into a target directory",
		Long: `Write semidx's bundled agent skills into a target directory.

Supported targets:

` + skillsTargetList() + `
Files carry a <!-- semidx-managed: skill --> marker. Re-running refreshes managed
skills; unmanaged same-name skills are left alone unless --force.

Pass --all to install into every user-level target (skips project). Failures are
reported and the remaining targets still run.`,
		Example: `  semidx skills install --target claude-code
  semidx skills install --target kimi
  semidx skills install --target agy
  semidx skills install --all
  semidx skills install --dir ./.claude/skills`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, _ := os.UserHomeDir()
			configDir, _ := os.UserConfigDir()
			if all {
				return installSkillsAll(cmd.OutOrStdout(), cmd.ErrOrStderr(), home, configDir, force)
			}
			dest, err := resolveSkillsDir(target, dir, home, configDir)
			if err != nil {
				return err
			}
			return installSkillsOne(cmd.OutOrStdout(), dest, force)
		},
	}
	c.Flags().StringVar(&target, "target", "claude-code", "Install target id (see --help for the full list)")
	c.Flags().StringVar(&dir, "dir", "", "Explicit destination directory (overrides --target)")
	c.Flags().BoolVar(&force, "force", false, "Overwrite unmanaged same-name skills")
	c.Flags().BoolVar(&all, "all", false, "Install into every user-level target")
	return c
}

func installSkillsOne(w io.Writer, dest string, force bool) error {
	written, err := skills.Install(dest, skills.InstallOptions{Force: force})
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Installed %d skill file(s) into %s:\n", len(written), dest)
	for _, f := range written {
		fmt.Fprintf(w, "  %s\n", f)
	}
	return nil
}

// installSkillsAll writes the skills into every user-level target. A failing
// target is reported and skipped so one unwritable home dir cannot abort the
// rest; the command still exits non-zero when any target failed.
func installSkillsAll(w, errW io.Writer, home, configDir string, force bool) error {
	var failed int
	for _, t := range userLevelSkillsTargets() {
		dest := t.path(home, configDir)
		if err := installSkillsOne(w, dest, force); err != nil {
			fmt.Fprintf(errW, "skipping %s (%s): %v\n", t.ID, dest, err)
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d target(s) failed", failed)
	}
	return nil
}

func newRepoAddCmd(d *deps) *cobra.Command {
	var name, branch, model string
	var index, wait bool
	var cred repoCredFlags
	c := &cobra.Command{
		Use:   "add <git-url>",
		Short: "Register a git repository and (optionally) start indexing it",
		Long: `Register a git repository with the server and, unless --index=false, queue a
full index job the server runs itself. Requires "semidx login".

Optional git credentials (admin-scoped token) can be stored inline on the new
project, or as a host-scoped fallback with --host-credential. Use --git-token -
to read an HTTPS token from stdin.`,
		Example: `  semidx repo add https://github.com/org/project.git
  semidx repo add https://github.com/org/project.git --branch main --wait
  semidx repo add https://github.com/org/private.git --git-user x --git-token -
  semidx repo add git@github.com:org/private.git --ssh-key ~/.ssh/deploy_ed25519
  semidx repo add https://gitea.lan/org/repo.git --host-credential --git-token "$TOKEN"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRepoAdd(cmd, d, repoAddOpts{
				gitURL: args[0], name: name, branch: branch, model: model,
				index: index, wait: wait, cred: cred,
			})
		},
	}
	c.Flags().StringVar(&name, "name", "", "Project name (default: repo basename)")
	c.Flags().StringVar(&branch, "branch", "", "Branch to index (default: server default)")
	c.Flags().StringVar(&model, "model", "bge-m3", "Embedding model")
	c.Flags().BoolVar(&index, "index", true, "Queue a full index job right away")
	c.Flags().BoolVar(&wait, "wait", false, "Wait for the queued index job and print live progress")
	c.Flags().StringVar(&cred.gitUser, "git-user", "", "HTTPS git username for a private repo")
	c.Flags().StringVar(&cred.gitToken, "git-token", "", "HTTPS token/password (- reads stdin)")
	c.Flags().StringVar(&cred.sshKey, "ssh-key", "", "SSH private key file (or - for stdin)")
	c.Flags().StringVar(&cred.sshKnownHosts, "ssh-known-hosts", "", "SSH known_hosts file (or - for stdin)")
	c.Flags().BoolVar(&cred.hostCredential, "host-credential", false, "Store credential for the repo host instead of the new project")
	return c
}

type repoAddOpts struct {
	gitURL, name, branch, model string
	index, wait                 bool
	cred                        repoCredFlags
}

func runRepoAdd(cmd *cobra.Command, d *deps, o repoAddOpts) error {
	if !d.remote() {
		return fmt.Errorf("repo add requires a server: run `semidx login` first")
	}
	if o.name == "" {
		o.name = strings.TrimSuffix(projectNameFromPath(o.gitURL), ".git")
	}
	cli := d.apiClient()
	ctx := cmd.Context()
	inlineCred, err := applyRepoAddCredential(ctx, cli, o.gitURL, o.cred)
	if err != nil {
		return err
	}
	if _, err := cli.CreateProjectWithParams(ctx, client.CreateProjectParams{
		Name: o.name, Model: o.model, SourceType: "git", GitURL: o.gitURL, Branch: o.branch,
		Credential: inlineCred,
	}); err != nil {
		return fmt.Errorf("create project: %w", err)
	}
	fmt.Printf("Registered git project %q -> %s\n", o.name, o.gitURL)
	if !o.index {
		return nil
	}
	jobID, err := cli.EnqueueJob(ctx, o.name, "full")
	if err != nil {
		return fmt.Errorf("enqueue index job: %w", err)
	}
	if !o.wait {
		fmt.Printf("Index job #%d queued. Poll it with: GET /api/v1/projects/%s/jobs/%d\n", jobID, o.name, jobID)
		return nil
	}
	fmt.Printf("Index job #%d queued — waiting for completion ...\n", jobID)
	job, err := waitForRemoteJobWithProgress(ctx, cli, o.name, jobID)
	if err != nil {
		return fmt.Errorf("wait for job %d: %w", jobID, err)
	}
	if job.Status == client.JobStatusFailed {
		return fmt.Errorf("job %d failed: %s", jobID, job.Error)
	}
	fmt.Printf("Done — indexed: %d, chunks: %d, errors: %d\n", job.FilesIndexed, job.ChunksCreated, job.ErrorCount)
	return nil
}

func waitForRemoteJobWithProgress(ctx context.Context, cli *client.Client, project string, jobID int) (*client.Job, error) {
	ticker := time.NewTicker(asyncPollInterval)
	defer ticker.Stop()

	var (
		lastStatus                                string
		lastDone, lastTotal, lastFiles, lastChunk int
		seen                                      bool
		lastJob                                   *client.Job
	)
	for {
		job, err := cli.GetJob(ctx, project, jobID)
		if err != nil {
			return nil, err
		}
		lastJob = job
		if shouldPrintJobProgress(job, seen, lastStatus, lastDone, lastTotal, lastFiles, lastChunk) {
			fmt.Println(formatJobProgress(job))
			lastStatus = job.Status
			lastDone = job.ProgressDone
			lastTotal = job.ProgressTotal
			lastFiles = job.FilesIndexed
			lastChunk = job.ChunksCreated
			seen = true
		}
		if job.Status == client.JobStatusSucceeded || job.Status == client.JobStatusFailed {
			return job, nil
		}
		select {
		case <-ctx.Done():
			if lastJob != nil {
				return lastJob, ctx.Err()
			}
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
