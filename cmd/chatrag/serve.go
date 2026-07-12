package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/config"
	"github.com/lgldsilva/semidx/internal/webchat"
)

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the ChatRAG HTTP server with web chat UI",
		Long: `Starts an HTTP server with a web-based chat UI for querying your
codebase. All flags are inherited from the root command.

  chatrag serve --project . --bind :8976

Requires the same setup as the interactive chat: an indexed project and a
chat model API key.`,
		RunE: runServe,
	}
}

func runServe(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// Read inherited flags from parent.
	localIndex, _ := cmd.Flags().GetString("local")
	project, _ := cmd.Flags().GetString("project")
	model, _ := cmd.Flags().GetString("model")
	bindAddr, _ := cmd.Flags().GetString("bind")

	cfg := config.Load()
	// nil approver: the web serve path uses the RAG pipeline, not the agent
	// Runner, so no action tools are wired here.
	pipeline, _, ls, resolvedProject, err := buildPipeline(ctx, cfg, localIndex, project, model, nil)
	if err != nil {
		return err
	}
	defer ls.Close()
	project = resolvedProject

	// Create the web chat server and start it.
	srv, err := webchat.New(pipeline, project, bindAddr)
	if err != nil {
		return fmt.Errorf("create web chat server: %w", err)
	}

	fmt.Fprintf(os.Stderr, "ChatRAG web server listening on %s\n", bindAddr)
	return srv.ListenAndServe()
}
