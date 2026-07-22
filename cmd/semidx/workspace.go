package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/clientconfig"
)

func newWorkspaceCmd(d *deps) *cobra.Command {
	c := &cobra.Command{Use: "workspace", Short: "Select and manage the active remote workspace"}
	c.AddCommand(newWorkspaceListCmd(d), newWorkspaceCreateCmd(d), newWorkspaceUseCmd(d))
	return c
}

func newWorkspaceListCmd(d *deps) *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: "List workspaces in the active tenant", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !d.hasServerConfig() {
				return fmt.Errorf("not logged in: run semidx login <server-url> --token <token>")
			}
			items, err := d.apiClient().ListWorkspaces(cmd.Context())
			if err != nil {
				return err
			}
			for _, item := range items {
				marker := " "
				if item.Slug == d.client.Workspace {
					marker = "*"
				}
				fmt.Printf("%s %-24s %s\n", marker, item.Slug, item.Name)
			}
			return nil
		},
	}
}

func newWorkspaceCreateCmd(d *deps) *cobra.Command {
	return &cobra.Command{
		Use: "create <slug> <name>", Short: "Create and select a workspace", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !d.hasServerConfig() {
				return fmt.Errorf("not logged in: run semidx login <server-url> --token <token>")
			}
			item, err := d.apiClient().CreateWorkspace(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			d.client.Workspace = item.Slug
			if err := clientconfig.Save(d.client); err != nil {
				return err
			}
			fmt.Printf("Created and selected workspace %s (%s)\n", item.Slug, item.Name)
			return nil
		},
	}
}

func newWorkspaceUseCmd(d *deps) *cobra.Command {
	return &cobra.Command{
		Use: "use <slug>", Short: "Select a workspace for subsequent remote commands", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if !d.hasServerConfig() {
				return fmt.Errorf("not logged in: run semidx login <server-url> --token <token>")
			}
			slug := strings.TrimSpace(args[0])
			if slug == "" {
				return fmt.Errorf("workspace slug is required")
			}
			d.client.Workspace = slug
			if err := clientconfig.Save(d.client); err != nil {
				return err
			}
			fmt.Printf("Selected workspace %s\n", slug)
			return nil
		},
	}
}
