package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/searchtargets"
	"github.com/lgldsilva/semidx/internal/store"
)

// newProjectsCmd lists indexed projects. The MCP server has always exposed
// semantic_projects, but the CLI had no equivalent, so a failed --project
// lookup left the caller with no way to discover a valid name.
func newProjectsCmd(d *deps) *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "projects",
		Short: "List indexed projects",
		Long: `List the projects available on the active backend, with their indexing
status and embedding model. Use it to find the name to pass to --project.`,
		Example: `  semidx projects
  semidx projects --json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			projects, err := listProjects(ctx, d)
			if err != nil {
				return err
			}
			sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(projects)
			}
			var b strings.Builder
			if len(projects) == 0 {
				b.WriteString("No indexed projects — run `semidx index --project .` (or `semidx push --project .`).\n")
			}
			for i := range projects {
				p := &projects[i]
				fmt.Fprintf(&b, "%-28s %-10s %-10s %s\n", p.Name, p.SourceType, p.Status, p.Model)
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), b.String())
			return err
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return c
}

func listProjects(ctx context.Context, d *deps) ([]store.Project, error) {
	if d.remote() {
		remote, err := d.apiClient().ListProjects(ctx)
		if err != nil {
			return nil, fmt.Errorf("list projects: %w", err)
		}
		return searchtargets.FromClientProjects(remote), nil
	}
	db, err := d.indexStore(ctx)
	if err != nil {
		return nil, err
	}
	return db.ListProjects(ctx, 0, 0)
}
