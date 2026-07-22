package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/privacy"
	"github.com/lgldsilva/semidx/internal/store"
)

func newPrivacyCmd(d *deps) *cobra.Command {
	var mode string
	c := &cobra.Command{
		Use:   "privacy PROJECT",
		Short: "Show or change a project's data-routing policy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("mode") {
				if d.remote() {
					p, err := d.apiClient().GetProject(cmd.Context(), args[0])
					if err != nil {
						return err
					}
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", projectPrivacy(p.PrivacyMode))
					return nil
				}
				st, err := d.indexStore(cmd.Context())
				if err != nil {
					return err
				}
				p, err := st.GetProject(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", projectPrivacy(p.PrivacyMode))
				return nil
			}
			validated, err := privacy.NormalizeMode(mode)
			if err != nil {
				return err
			}
			if d.remote() {
				p, err := d.apiClient().SetProjectPrivacy(cmd.Context(), args[0], string(validated))
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", projectPrivacy(p.PrivacyMode))
				return nil
			}
			st, err := d.indexStore(cmd.Context())
			if err != nil {
				return err
			}
			p, err := st.GetProject(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			policy, ok := st.(store.ProjectPolicyStore)
			if !ok {
				return fmt.Errorf("project privacy policies are unavailable")
			}
			if err := policy.SetProjectPrivacy(cmd.Context(), p.ID, string(validated)); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", validated)
			return nil
		},
	}
	c.Flags().StringVar(&mode, "mode", "", "set policy: cloud, hybrid, or edge")
	return c
}

func projectPrivacy(mode string) string {
	if strings.TrimSpace(mode) == "" {
		return string(privacy.Hybrid)
	}
	return mode
}
