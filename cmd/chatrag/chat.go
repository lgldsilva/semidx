package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/lgldsilva/semidx/internal/agent"
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
	pipeline, agt, ls, resolvedProject, err := buildPipeline(ctx, cfg, localIndex, project, model)
	if err != nil {
		return err
	}
	defer ls.Close()

	// Run the interactive REPL.
	return runREPL(ctx, pipeline, agt, resolvedProject)
}

// runREPL is the interactive chat loop.
// When agt is non-nil, the agent loop is used (supports tool calling).
func runREPL(ctx context.Context, pipeline *rag.Pipeline, agt *agent.Agent, project string) error {
	history := chat.NewHistory(10) // keep up to 10 turns
	scanner := bufio.NewScanner(os.Stdin)

	mode := "RAG"
	if agt != nil {
		mode = "agent"
	}
	fmt.Printf("ChatRAG [%s mode] — ask questions about your codebase. Type /exit to quit, /clear to reset.\n", mode)
	if project != "" {
		fmt.Printf("Project: %s\n", project)
	}
	fmt.Println()

	for {
		fmt.Printf("[%s] > ", mode)
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())

		if handled, cont := handleREPLCommand(line, history, &mode, agt != nil); handled {
			if cont {
				continue
			}
			return nil
		}
		if handleREPLTurn(ctx, pipeline, agt, project, line, history, mode) {
			continue
		}
	}

	return scanner.Err()
}

// handleREPLCommand processes meta-commands (empty, exit, clear, mode).
// handled=true when line matched a command; cont=false means stop the REPL.
func handleREPLCommand(line string, history *chat.History, mode *string, hasAgent bool) (handled, cont bool) {
	if line == "" {
		return true, true
	}
	switch line {
	case "/exit", "/quit":
		fmt.Println("Goodbye!")
		return true, false
	case "/clear":
		history.Clear()
		fmt.Println("History cleared.")
		return true, true
	case "/mode":
		if *mode == "agent" {
			*mode = "RAG"
		} else if hasAgent {
			*mode = "agent"
		} else {
			*mode = "RAG"
		}
		fmt.Printf("Switched to %s mode.\n", *mode)
		return true, true
	}
	return false, false
}

// handleREPLTurn dispatches a question to agent or RAG. Returns true on error.
func handleREPLTurn(ctx context.Context, pipeline *rag.Pipeline, agt *agent.Agent, project, line string, history *chat.History, mode string) bool {
	if agt != nil && mode == "agent" {
		if handleAgentReply(ctx, agt, line, history) {
			return true
		}
	} else {
		if handleRAGReply(ctx, pipeline, line, project, history) {
			return true
		}
	}
	return false
}

// handleAgentReply processes an agent-mode answer: calls the agent, updates
// history, and prints the answer and tool trace. Returns true on error (caller
// should continue the REPL loop).
func handleAgentReply(ctx context.Context, agt *agent.Agent, line string, history *chat.History) bool {
	answer, err := agt.Ask(ctx, line, history.GetMessages())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", formatError(err))
		return true
	}

	history.AddUser(line)
	history.AddAssistant(answer.Content)

	fmt.Println()
	fmt.Println(answer.Content)
	fmt.Println()

	printAgentTrace(answer.Trace)
	fmt.Printf("[agent mode | model: %s]\n\n", answer.Model)
	return false
}

// handleRAGReply processes a RAG-mode answer: calls the pipeline, updates
// history, and prints the answer and sources. Returns true on error (caller
// should continue the REPL loop).
func handleRAGReply(ctx context.Context, pipeline *rag.Pipeline, line, project string, history *chat.History) bool {
	answer, err := pipeline.Ask(ctx, line, project, history.GetMessages())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", formatError(err))
		return true
	}

	history.AddUser(line)
	history.AddAssistant(answer.Content)

	fmt.Println()
	fmt.Println(answer.Content)
	fmt.Println()

	printSources(answer.Sources)
	fmt.Printf("[model: %s]\n\n", formatModelInfo(answer.Model, answer.Fallback))
	return false
}

// printAgentTrace prints the tool call trace (if any) from an agent answer.
func printAgentTrace(trace []agent.ToolCallRecord) {
	if len(trace) == 0 {
		return
	}
	fmt.Println("Tool calls:")
	for _, tc := range trace {
		errInfo := ""
		if tc.Error != "" {
			errInfo = fmt.Sprintf(" error=%s", tc.Error)
		}
		fmt.Printf("  • %s(%s)%s\n", tc.Tool, tc.Args, errInfo)
	}
	fmt.Println()
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

// printSources prints the Sources section (if any) with keyword vs scored labeling.
// Extracted to reduce cognitive complexity of runREPL.
func printSources(sources []rag.Source) {
	if len(sources) == 0 {
		return
	}
	fmt.Println("Sources:")
	for _, src := range sources {
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

// formatModelInfo formats the trailing [model: ...] line, handling empty and fallback.
// Extracted to reduce cognitive complexity of runREPL.
func formatModelInfo(model string, fallback bool) string {
	if model == "" {
		model = "unknown"
	}
	if fallback {
		model += " (keyword fallback)"
	}
	return model
}
