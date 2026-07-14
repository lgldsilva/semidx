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
	"github.com/lgldsilva/semidx/internal/permission"
	"github.com/lgldsilva/semidx/internal/rag"
	"github.com/lgldsilva/semidx/internal/store"
)

const defaultChatModel = "gemini-2.5-flash"

// runChat builds the dependency chain and then starts the interactive REPL.
// It accepts a cobra command's context and the parsed flag values.
func runChat(ctx context.Context, localIndex, project, model string) error {
	cfg := config.Load()
	// One stdin scanner is shared by the REPL loop and the action-tool approval
	// prompt so their reads don't fight over buffered input.
	scanner := bufio.NewScanner(os.Stdin)
	approve := makePromptApprover(scanner)
	pipeline, runner, ls, resolvedProject, err := buildPipeline(ctx, cfg, localIndex, project, model, approve)
	if err != nil {
		return err
	}
	defer ls.Close()

	// Run the interactive REPL.
	return runREPL(ctx, pipeline, runner, resolvedProject, scanner)
}

// makePromptApprover returns an approval gate that asks the user y/N on the
// shared stdin scanner before an action tool executes.
func makePromptApprover(scanner *bufio.Scanner) permission.Approver {
	return func(_ context.Context, req permission.Request) (bool, error) {
		fmt.Printf("\n⚠  The agent wants to run %s\n   %s\n   Approve? [y/N] ", req.Tool, req.Detail)
		if !scanner.Scan() {
			return false, nil
		}
		ans := strings.ToLower(strings.TrimSpace(scanner.Text()))
		fmt.Println()
		return ans == "y" || ans == "yes", nil
	}
}

// runREPL is the interactive chat loop.
// When runner is non-nil, the agent loop is used (supports tool calling).
func runREPL(ctx context.Context, pipeline *rag.FantasyPipeline, runner *agent.Runner, project string, scanner *bufio.Scanner) error {
	history := chat.NewHistory(10)     // RAG mode: text-only turns
	convo := agent.NewConversation(10) // agent mode: full tool_call/tool-result memory

	mode := "RAG"
	if runner != nil {
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

		if handled, cont := handleREPLCommand(line, history, convo, &mode, runner != nil); handled {
			if cont {
				continue
			}
			return nil
		}
		if handleREPLTurn(replTurn{
			ctx: ctx, pipeline: pipeline, runner: runner, project: project,
			line: line, history: history, convo: convo, mode: mode,
		}) {
			continue
		}
	}

	return scanner.Err()
}

// handleREPLCommand processes meta-commands (empty, exit, clear, mode).
// handled=true when line matched a command; cont=false means stop the REPL.
func handleREPLCommand(line string, history *chat.History, convo *agent.Conversation, mode *string, hasAgent bool) (handled, cont bool) {
	if line == "" {
		return true, true
	}
	switch line {
	case "/exit", "/quit":
		fmt.Println("Goodbye!")
		return true, false
	case "/clear":
		history.Clear()
		convo.Clear()
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

// replTurn bundles one REPL question dispatch.
type replTurn struct {
	ctx      context.Context
	pipeline *rag.FantasyPipeline
	runner   *agent.Runner
	project  string
	line     string
	history  *chat.History
	convo    *agent.Conversation
	mode     string
}

// handleREPLTurn dispatches a question to agent or RAG. Returns true on error.
func handleREPLTurn(t replTurn) bool {
	if t.runner != nil && t.mode == "agent" {
		if handleAgentReply(t.ctx, t.runner, t.line, t.convo) {
			return true
		}
	} else if handleRAGReply(t.ctx, t.pipeline, t.line, t.project, t.history) {
		return true
	}
	return false
}

// handleAgentReply streams an agent-mode answer live: the assistant text prints
// as it arrives and tool calls are announced inline, using the full multi-turn
// conversation (so the model sees prior tool_calls and their results). It then
// records the turn and prints the token usage. Returns true on error (caller
// should continue the REPL loop).
func handleAgentReply(ctx context.Context, runner *agent.Runner, line string, convo *agent.Conversation) bool {
	fmt.Println()
	cb := agent.StreamCallbacks{
		OnText: func(delta string) { fmt.Print(delta) },
		OnToolCall: func(_, name, input string) {
			fmt.Printf("\n  ⟳ %s(%s)\n", name, truncateArg(input, 120))
		},
		OnToolResult: func(_, name, result string, isError bool) {
			if isError {
				fmt.Printf("  ✗ %s: %s\n", name, truncateArg(result, 120))
			}
		},
	}
	answer, err := runner.Stream(ctx, line, convo.Messages(), cb)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %s\n", formatError(err))
		return true
	}

	// Persist the full turn — the user message plus every assistant/tool
	// message — so the next turn keeps real tool memory.
	convo.AddUser(line)
	convo.AddTurnMessages(answer.Messages)

	fmt.Printf("\n\n[agent mode | model: %s | %s]\n\n", answer.Model, formatUsage(answer.Usage))
	return false
}

// truncateArg shortens a tool argument/result string for a single-line status
// notice, appending an ellipsis when it overflows.
func truncateArg(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// handleRAGReply processes a RAG-mode answer: calls the pipeline, updates
// history, and prints the answer and sources. Returns true on error (caller
// should continue the REPL loop).
func handleRAGReply(ctx context.Context, pipeline *rag.FantasyPipeline, line, project string, history *chat.History) bool {
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

// formatUsage renders token accounting for the trailing agent-mode status line.
// It stays quiet (n/a) when a provider reports no usage, and surfaces the
// prompt-cache split only when the provider actually used the cache.
func formatUsage(u agent.Usage) string {
	if u.InputTokens == 0 && u.OutputTokens == 0 && u.TotalTokens == 0 {
		return "tokens: n/a"
	}
	s := fmt.Sprintf("tokens: in=%d out=%d total=%d", u.InputTokens, u.OutputTokens, u.TotalTokens)
	if u.CacheReadTokens > 0 || u.CacheCreationTokens > 0 {
		s += fmt.Sprintf(" cache(r=%d w=%d)", u.CacheReadTokens, u.CacheCreationTokens)
	}
	return s
}

// formatError returns a clean, actionable error message for the REPL.
func formatError(err error) string {
	// Context cancellation.
	if errors.Is(err, context.Canceled) {
		return "request cancelled"
	}

	// Project-not-found from the search adapter.
	if errors.Is(err, store.ErrNotFound) {
		return "project not found — is it indexed? Run: semidx index --project <path>"
	}

	// Fantasy/LLM failures are already wrapped with context by the pipeline.
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
