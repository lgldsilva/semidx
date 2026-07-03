package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/clientconfig"
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
		Project:  &store.Project{Name: cr.Project},
		Model:    cr.Model,
		Results:  results,
		Fallback: cr.Fallback,
	}
}

// runSearch executes a query against the configured server (remote mode) or the
// local database (embedded), returning a uniform response plus how long it took.
func (d *deps) runSearch(cmd *cobra.Command, project, query, model string, topK int, forcePrivacy bool) (*search.Response, time.Duration, error) {
	ctx := cmd.Context()
	if d.remote() {
		resp, err := d.apiClient().Search(ctx, project, query, model, topK)
		if err != nil {
			return nil, 0, err
		}
		return remoteToResponse(resp), time.Duration(resp.TookMS) * time.Millisecond, nil
	}

	d.applyPrivacy(forcePrivacy)
	db, err := d.indexStore(ctx)
	if err != nil {
		return nil, 0, err
	}
	start := time.Now()
	resp, err := search.NewService(db, d.emb).Search(ctx, search.Request{
		Project: project, Query: query, Model: model, TopK: topK, KeywordOnly: d.cfg.KeywordOnly,
	})
	return resp, time.Since(start), err
}

// newLoginCmd stores the server URL + token in the client config and verifies the
// server is reachable.
func newLoginCmd(d *deps) *cobra.Command {
	var token, defaultProject string
	c := &cobra.Command{
		Use:   "login <server-url>",
		Short: "Save credentials for a semidx server and verify reachability",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if token == "" {
				token = os.Getenv("SEMIDX_TOKEN")
			}
			if token == "" {
				return fmt.Errorf("a token is required: pass --token or set SEMIDX_TOKEN")
			}
			cfg := &clientconfig.Config{ServerURL: args[0], Token: token, DefaultProject: defaultProject}
			d.client = cfg
			if err := d.apiClient().Healthz(cmd.Context()); err != nil {
				return fmt.Errorf("cannot reach server at %s: %w", args[0], err)
			}
			if err := clientconfig.Save(cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			path, _ := clientconfig.Path()
			fmt.Printf("Logged in to %s (saved to %s)\n", args[0], path)
			return nil
		},
	}
	c.Flags().StringVar(&token, "token", "", "API token (or set SEMIDX_TOKEN)")
	c.Flags().StringVar(&defaultProject, "default-project", "", "Default project for search/sgrep")
	return c
}

// newRepoCmd groups server-side repository management (git projects the server
// clones and indexes itself).
func newRepoCmd(d *deps) *cobra.Command {
	c := &cobra.Command{
		Use:   "repo",
		Short: "Manage git repositories indexed by the server",
	}
	c.AddCommand(newRepoAddCmd(d))
	return c
}

// newSkillsCmd manages the embedded agent skills.
func newSkillsCmd(_ *deps) *cobra.Command {
	c := &cobra.Command{
		Use:   "skills",
		Short: "Install the agent skills that teach AI assistants to use semidx",
	}
	c.AddCommand(newSkillsInstallCmd())
	return c
}

func newSkillsInstallCmd() *cobra.Command {
	var target, dir string
	c := &cobra.Command{
		Use:   "install",
		Short: "Write the bundled skills into a target directory",
		RunE: func(_ *cobra.Command, _ []string) error {
			dest, err := resolveSkillsDir(target, dir)
			if err != nil {
				return err
			}
			written, err := skills.Install(dest)
			if err != nil {
				return err
			}
			fmt.Printf("Installed %d skill file(s) into %s:\n", len(written), dest)
			for _, w := range written {
				fmt.Printf("  %s\n", w)
			}
			return nil
		},
	}
	c.Flags().StringVar(&target, "target", "claude-code", "Install target: claude-code (~/.claude/skills) or project (./.claude/skills)")
	c.Flags().StringVar(&dir, "dir", "", "Explicit destination directory (overrides --target)")
	return c
}

// resolveSkillsDir maps a --target keyword (or an explicit --dir) to a skills
// directory.
func resolveSkillsDir(target, dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	switch target {
	case "claude-code":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".claude", "skills"), nil
	case "project":
		return filepath.Join(".claude", "skills"), nil
	default:
		return "", fmt.Errorf("unknown --target %q (use claude-code or project, or pass --dir)", target)
	}
}

func newRepoAddCmd(d *deps) *cobra.Command {
	var name, branch, model string
	var index bool
	c := &cobra.Command{
		Use:   "add <git-url>",
		Short: "Register a git repository and (optionally) start indexing it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !d.remote() {
				return fmt.Errorf("repo add requires a server: run `semidx login` first")
			}
			gitURL := args[0]
			if name == "" {
				name = strings.TrimSuffix(projectNameFromPath(gitURL), ".git")
			}
			cli := d.apiClient()
			ctx := cmd.Context()
			if _, err := cli.CreateProject(ctx, name, model, "git", gitURL, branch); err != nil {
				return fmt.Errorf("create project: %w", err)
			}
			fmt.Printf("Registered git project %q -> %s\n", name, gitURL)
			if !index {
				return nil
			}
			jobID, err := cli.EnqueueJob(ctx, name, "full")
			if err != nil {
				return fmt.Errorf("enqueue index job: %w", err)
			}
			fmt.Printf("Index job #%d queued. Poll it with: GET /api/v1/jobs/%d\n", jobID, jobID)
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "Project name (default: repo basename)")
	c.Flags().StringVar(&branch, "branch", "", "Branch to index (default: server default)")
	c.Flags().StringVar(&model, "model", "bge-m3", "Embedding model")
	c.Flags().BoolVar(&index, "index", true, "Queue a full index job right away")
	return c
}
