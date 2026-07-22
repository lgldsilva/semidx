package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/depresolve"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/pkg/client"
)

func newDependenciesCmd(d *deps) *cobra.Command {
	cmd := &cobra.Command{Use: "deps", Short: "Inspect project dependencies and shared packages"}
	cmd.AddCommand(newDependenciesListCmd(d), newDependenciesSharedCmd(d), newDependenciesResolveCmd(d))
	return cmd
}

func newDependenciesResolveCmd(d *deps) *cobra.Command {
	var asJSON bool
	var mode string
	cmd := &cobra.Command{
		Use:   "resolve PROJECT",
		Short: "Run the native dependency resolver and refresh the local catalog",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if d.remote() {
				if mode == "agent" {
					root := args[0]
					if info, statErr := os.Stat(root); statErr != nil || !info.IsDir() {
						root, statErr = os.Getwd()
						if statErr != nil {
							return statErr
						}
					}
					resolved, resolveErr := depresolve.New().ResolveProject(cmd.Context(), filepath.Clean(root))
					if resolveErr != nil {
						return resolveErr
					}
					deps := make([]client.Dependency, 0, len(resolved))
					for _, dep := range resolved {
						deps = append(deps, client.Dependency{Ecosystem: string(dep.Ecosystem), Name: dep.Name, NormalizedName: dep.NormalizedName, Constraint: dep.Constraint, ResolvedVersion: dep.ResolvedVersion, Scope: dep.Scope, Source: dep.Source, Manifest: dep.Manifest, Direct: dep.Direct})
					}
					out, submitErr := d.apiClient().SubmitDependencies(cmd.Context(), args[0], deps, "customer-agent")
					if submitErr != nil {
						return submitErr
					}
					if asJSON {
						return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
					}
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%d\n", out.Project, out.Status, out.Count); err != nil {
						return err
					}
					return nil
				}
				out, resolveErr := d.apiClient().ResolveDependencies(cmd.Context(), args[0], mode)
				if resolveErr != nil {
					return resolveErr
				}
				if asJSON {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%d\n", out.Project, out.Mode, out.Status, out.JobID); err != nil {
					return err
				}
				return nil
			}
			st, err := d.indexStore(cmd.Context())
			if err != nil {
				return err
			}
			depStore, ok := st.(store.DependencyStore)
			if !ok {
				return fmt.Errorf("dependency catalog is unavailable")
			}
			project, err := st.GetProject(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if project.Path == "" {
				return fmt.Errorf("project %q has no local path", project.Name)
			}
			resolved, err := depresolve.New().ResolveProject(cmd.Context(), project.Path)
			if err != nil {
				return err
			}
			deps := make([]store.Dependency, 0, len(resolved))
			for _, dep := range resolved {
				deps = append(deps, store.Dependency{Ecosystem: string(dep.Ecosystem), Name: dep.Name, NormalizedName: dep.NormalizedName, Constraint: dep.Constraint, ResolvedVersion: dep.ResolvedVersion, Scope: dep.Scope, Source: dep.Source, Manifest: dep.Manifest, Direct: dep.Direct})
			}
			if err := depStore.ReplaceProjectDependencies(cmd.Context(), project.ID, deps); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(resolved)
			}
			for _, dep := range resolved {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", dep.Ecosystem, dep.Name, firstNonEmpty(dep.ResolvedVersion, dep.Constraint), dep.Scope); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.Flags().StringVar(&mode, "mode", "managed", "remote resolver mode: managed or agent")
	return cmd
}

func newDependenciesListCmd(d *deps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list PROJECT",
		Short: "List normalized manifest dependencies",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if d.remote() {
				deps, err := d.apiClient().ListDependencies(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				return printClientDependencies(cmd.OutOrStdout(), deps, asJSON)
			}
			st, err := d.indexStore(cmd.Context())
			if err != nil {
				return err
			}
			depStore, ok := st.(store.DependencyStore)
			if !ok {
				return fmt.Errorf("dependency catalog is unavailable")
			}
			project, err := st.GetProject(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			deps, err := depStore.ListProjectDependencies(cmd.Context(), project.ID)
			if err != nil {
				return err
			}
			return printStoreDependencies(cmd.OutOrStdout(), deps, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func newDependenciesSharedCmd(d *deps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "shared PROJECT",
		Short: "Find packages also used by other projects in the tenant",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if d.remote() {
				deps, err := d.apiClient().SharedDependencies(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				if asJSON {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(deps)
				}
				for _, dep := range deps {
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", dep.ProjectName, dep.Ecosystem, dep.Name, firstNonEmpty(dep.ResolvedVersion, dep.Constraint)); err != nil {
						return err
					}
				}
				return nil
			}
			st, err := d.indexStore(cmd.Context())
			if err != nil {
				return err
			}
			depStore, ok := st.(store.DependencyStore)
			if !ok {
				return fmt.Errorf("dependency catalog is unavailable")
			}
			project, err := st.GetProject(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			deps, err := depStore.FindProjectsSharingDependency(cmd.Context(), project.ID)
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(deps)
			}
			for _, dep := range deps {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", dep.ProjectName, dep.Ecosystem, dep.Name, firstNonEmpty(dep.ResolvedVersion, dep.Constraint)); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func printClientDependencies(out io.Writer, deps []client.Dependency, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(out).Encode(deps)
	}
	for _, dep := range deps {
		if _, err := fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", dep.Ecosystem, dep.Name, firstNonEmpty(dep.ResolvedVersion, dep.Constraint), dep.Scope); err != nil {
			return err
		}
	}
	return nil
}

func printStoreDependencies(out io.Writer, deps []store.Dependency, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(out).Encode(deps)
	}
	for _, dep := range deps {
		if _, err := fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", dep.Ecosystem, dep.Name, firstNonEmpty(dep.ResolvedVersion, dep.Constraint), dep.Scope); err != nil {
			return err
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
