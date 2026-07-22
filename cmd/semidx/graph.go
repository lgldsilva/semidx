package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/pkg/client"
)

func newGraphCmd(d *deps) *cobra.Command {
	c := &cobra.Command{Use: "graph", Short: "Inspect static and observed project communication"}
	c.AddCommand(newRuntimeGraphCmd(d), newPortfolioGraphCmd(d))
	return c
}

func newRuntimeGraphCmd(d *deps) *cobra.Command {
	var input, target, protocol, environment string
	var asJSON bool
	c := &cobra.Command{
		Use:   "runtime PROJECT",
		Short: "List or submit observed runtime communication edges",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if input == "" {
				if d.remote() {
					edges, err := d.apiClient().ListRuntimeEdges(cmd.Context(), args[0])
					if err != nil {
						return err
					}
					return printRuntimeEdges(cmd.OutOrStdout(), edges, asJSON)
				}
				st, err := d.indexStore(cmd.Context())
				if err != nil {
					return err
				}
				graph, ok := st.(store.RuntimeGraphStore)
				if !ok {
					return fmt.Errorf("runtime graph is unavailable")
				}
				p, err := st.GetProject(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				edges, err := graph.ListRuntimeEdges(cmd.Context(), p.ID)
				if err != nil {
					return err
				}
				return printRuntimeEdges(cmd.OutOrStdout(), toClientEdges(edges), asJSON)
			}
			edges, err := readRuntimeEdges(input)
			if err != nil {
				return err
			}
			for i := range edges {
				if target != "" {
					edges[i].TargetProjectName = target
				}
				if protocol != "" {
					edges[i].Protocol = protocol
				}
				if environment != "" {
					edges[i].Environment = environment
				}
			}
			if d.remote() {
				accepted, err := d.apiClient().SubmitRuntimeEdges(cmd.Context(), args[0], edges)
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "accepted\t%d\n", accepted)
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
			graph, ok := st.(store.RuntimeGraphStore)
			if !ok {
				return fmt.Errorf("runtime graph is unavailable")
			}
			if err := graph.UpsertRuntimeEdges(cmd.Context(), p.ID, toStoreEdges(edges)); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "accepted\t%d\n", len(edges))
			return nil
		},
	}
	c.Flags().StringVarP(&input, "input", "i", "", "JSON file with {\"edges\": [...]} or an edge array")
	c.Flags().StringVar(&target, "target", "", "override target project/service for every input edge")
	c.Flags().StringVar(&protocol, "protocol", "", "override protocol (HTTP, gRPC, Kafka, …)")
	c.Flags().StringVar(&environment, "environment", "", "override environment (prod, staging, …)")
	c.Flags().BoolVar(&asJSON, "json", false, "print JSON when listing")
	return c
}

func newPortfolioGraphCmd(d *deps) *cobra.Command {
	var asJSON bool
	return &cobra.Command{
		Use:   "portfolio",
		Short: "List observed communication across the active workspace",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if d.remote() {
				edges, err := d.apiClient().ListRuntimeGraph(cmd.Context(), 500)
				if err != nil {
					return err
				}
				return printRuntimeEdges(cmd.OutOrStdout(), edges, asJSON)
			}
			st, err := d.indexStore(cmd.Context())
			if err != nil {
				return err
			}
			graph, ok := st.(store.RuntimeGraphStore)
			if !ok {
				return fmt.Errorf("runtime graph is unavailable")
			}
			edges, err := graph.ListWorkspaceRuntimeEdges(cmd.Context(), 500)
			if err != nil {
				return err
			}
			return printRuntimeEdges(cmd.OutOrStdout(), toClientEdges(edges), asJSON)
		},
	}
}

func readRuntimeEdges(path string) ([]client.RuntimeEdge, error) {
	f, err := os.Open(path) // #nosec G304 -- graph --input intentionally reads the user-selected JSON file.
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var wrapped struct {
		Edges []client.RuntimeEdge `json:"edges"`
	}
	data, err := io.ReadAll(io.LimitReader(f, 5<<20))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.Edges != nil {
		return wrapped.Edges, nil
	}
	var edges []client.RuntimeEdge
	if err := json.Unmarshal(data, &edges); err != nil {
		return nil, fmt.Errorf("parse runtime graph JSON: %w", err)
	}
	return edges, nil
}

func toClientEdges(edges []store.RuntimeEdge) []client.RuntimeEdge {
	out := make([]client.RuntimeEdge, 0, len(edges))
	for _, edge := range edges {
		out = append(out, client.RuntimeEdge{
			TenantID: edge.TenantID, WorkspaceID: edge.WorkspaceID,
			SourceProjectID: edge.SourceProjectID, SourceProjectName: edge.SourceProjectName,
			TargetProjectID: edge.TargetProjectID, TargetProjectName: edge.TargetProjectName,
			SourceComponent: edge.SourceComponent, TargetComponent: edge.TargetComponent,
			Protocol: edge.Protocol, Environment: edge.Environment,
			RequestCount: edge.RequestCount, ErrorCount: edge.ErrorCount,
			P95LatencyMS: edge.P95LatencyMS, FirstSeen: edge.FirstSeen, LastSeen: edge.LastSeen,
		})
	}
	return out
}

func toStoreEdges(edges []client.RuntimeEdge) []store.RuntimeEdge {
	out := make([]store.RuntimeEdge, 0, len(edges))
	for _, edge := range edges {
		out = append(out, store.RuntimeEdge{
			TargetProjectID: edge.TargetProjectID, TargetProjectName: edge.TargetProjectName,
			SourceComponent: edge.SourceComponent, TargetComponent: edge.TargetComponent,
			Protocol: edge.Protocol, Environment: edge.Environment,
			RequestCount: edge.RequestCount, ErrorCount: edge.ErrorCount,
			P95LatencyMS: edge.P95LatencyMS, FirstSeen: edge.FirstSeen, LastSeen: edge.LastSeen,
		})
	}
	return out
}

func printRuntimeEdges(w io.Writer, edges []client.RuntimeEdge, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(w).Encode(edges)
	}
	for _, edge := range edges {
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%.1fms\n", edge.SourceProjectName, edge.TargetProjectName, strings.TrimSpace(edge.Protocol), edge.RequestCount, edge.ErrorCount, edge.P95LatencyMS); err != nil {
			return err
		}
	}
	return nil
}
