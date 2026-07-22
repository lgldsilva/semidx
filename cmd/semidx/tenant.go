package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/clientconfig"
)

func newTenantCmd(d *deps) *cobra.Command {
	c := &cobra.Command{
		Use:   "tenant",
		Short: "Select and manage the active remote tenant",
	}
	c.AddCommand(newTenantListCmd(d), newTenantCreateCmd(d), newTenantUseCmd(d))
	c.AddCommand(newTenantUsageCmd(d))
	return c
}

func newTenantUsageCmd(d *deps) *cobra.Command {
	return &cobra.Command{
		Use:   "usage",
		Short: "Show active tenant quota and usage counters",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !d.hasServerConfig() {
				return fmt.Errorf("not logged in: run semidx login <server-url> --token <token>")
			}
			out, err := d.apiClient().Usage(cmd.Context())
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "plan\t%s\nprojects\t%d/%d\nruntime_edges\t%d/%d\n",
				out.Quota.Plan, out.Usage.Projects, out.Quota.MaxProjects,
				out.Usage.RuntimeEdges, out.Quota.MaxRuntimeEdges)
			return nil
		},
	}
}

func newTenantListCmd(d *deps) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List tenants visible to the authenticated user",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !d.hasServerConfig() {
				return fmt.Errorf("not logged in: run semidx login <server-url> --token <token>")
			}
			tenants, err := d.apiClient().ListTenants(cmd.Context())
			if err != nil {
				return err
			}
			for _, t := range tenants {
				marker := " "
				if t.Slug == d.client.Tenant {
					marker = "*"
				}
				fmt.Printf("%s %-24s %s\n", marker, t.Slug, t.Name)
			}
			return nil
		},
	}
}

func newTenantCreateCmd(d *deps) *cobra.Command {
	return &cobra.Command{
		Use:   "create <slug> <name>",
		Short: "Create a tenant and make it active",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !d.hasServerConfig() {
				return fmt.Errorf("not logged in: run semidx login <server-url> --token <token>")
			}
			t, err := d.apiClient().CreateTenant(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			d.client.Tenant = t.Slug
			if err := clientconfig.Save(d.client); err != nil {
				return err
			}
			fmt.Printf("Created and selected tenant %s (%s)\n", t.Slug, t.Name)
			return nil
		},
	}
}

func newTenantUseCmd(d *deps) *cobra.Command {
	return &cobra.Command{
		Use:   "use <slug>",
		Short: "Select a tenant for subsequent remote commands",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if !d.hasServerConfig() {
				return fmt.Errorf("not logged in: run semidx login <server-url> --token <token>")
			}
			slug := strings.TrimSpace(args[0])
			if slug == "" {
				return fmt.Errorf("tenant slug is required")
			}
			d.client.Tenant = slug
			if err := clientconfig.Save(d.client); err != nil {
				return err
			}
			fmt.Printf("Selected tenant %s\n", slug)
			return nil
		},
	}
}
