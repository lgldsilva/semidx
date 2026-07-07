package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/lgldsilva/semidx/internal/chat"
	"github.com/lgldsilva/semidx/internal/config"
	"github.com/lgldsilva/semidx/internal/rag"
	"github.com/lgldsilva/semidx/internal/store"
)

const defaultChatModel = "gemini-2.5-flash"

// runChat builds the dependency chain and then starts the interactive REPL.
// It accepts a cobra command's context and the parsed flag values.
func runChat(ctx context.Context, localIndex, project, model string) error {
	cfg := config.Load()
	pipeline, ls, resolvedProject, err := buildPipeline(ctx, cfg, localIndex, project, model)
	if err != nil {
		return err
	}
	defer ls.Close()

	// Run the interactive REPL.
	return runREPL(ctx, pipeline, resolvedProject)
}

// runREPL is the interactive chat loop.
func runREPL(ctx context.Context, pipeline *rag.Pipeline, project string) error {
	history := chat.NewHistory(10) // keep up to 10 turns
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("ChatRAG — ask questions about your codebase. Type /exit to quit, /clear to reset.")
	if project != "" {
		fmt.Printf("Project: %s\n", project)
	}
	fmt.Println()

	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())

		// Handle commands.
		switch line {
		case "":
			continue
		case "/exit", "/quit":
			fmt.Println("Goodbye!")
			return nil
		case "/clear":
			history.Clear()
			fmt.Println("History cleared.")
			continue
		}

		// B1: Run the RAG pipeline first. Only add to history on success.
		answer, err := pipeline.Ask(ctx, line, project, history.GetMessages())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", formatError(err))
			// Failed turn is NOT committed to history.
			continue
		}

		// B1: Now commit the turn to history.
		history.AddUser(line)
		history.AddAssistant(answer.Content)

		// Print the answer.
		fmt.Println()
		fmt.Println(answer.Content)
		fmt.Println()

		// N7: Print sources with proper labeling for keyword matches.
		if len(answer.Sources) > 0 {
			fmt.Println("Sources:")
			for _, src := range answer.Sources {
				if src.Keyword {
					fmt.Printf("  %s:%d-%d [keyword match]\n",
						src.File, src.StartLine, src.EndLine)
				} else {
					fmt.Printf("  %s:%d-%d (%.3f)\n",
						src.File, src.StartLine, src.EndLine, src.Score)
				}
			}
			fmt.Println()
		}

		// Print model info.
		modelInfo := answer.Model
		if modelInfo == "" {
			modelInfo = "unknown"
		}
		if answer.Fallback {
			modelInfo += " (keyword fallback)"
		}
		fmt.Printf("[model: %s]\n\n", modelInfo)
	}

	return scanner.Err()
}

// formatError returns a clean, actionable error message for the REPL.
func formatError(err error) string {
	// HTTPError from a chat provider — use its user-friendly Error().
	var httpErr *chat.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Error()
	}

	// Context cancellation.
	if errors.Is(err, context.Canceled) {
		return "request cancelled"
	}

	// Project-not-found from the search adapter.
	if errors.Is(err, store.ErrNotFound) {
		return "project not found — is it indexed? Run: semidx index --project <path>"
	}

	// For everything else (chain summary, search errors, etc.) the error
	// message is already actionable because we wrapped it with context.
	return err.Error()
}
