package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/chat"
	"github.com/lgldsilva/semidx/internal/config"
	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/llm"
	"github.com/lgldsilva/semidx/internal/localstore"
	"github.com/lgldsilva/semidx/internal/permission"
	"github.com/lgldsilva/semidx/internal/projectref"
	"github.com/lgldsilva/semidx/internal/rag"
	"github.com/lgldsilva/semidx/internal/search"
)

// buildPipeline constructs the full RAG pipeline and its dependencies.
// Caller must close the returned SQLiteStore when done.
// Returns the pipeline, optional agent Runner (nil when tool calling unavailable), store, and resolved project name.
// approve is the action-tool approval gate (nil disables the action tools).
func buildPipeline(ctx context.Context, cfg *config.Config, indexPath, project, model string, approve permission.Approver) (*rag.Pipeline, *agent.Runner, *localstore.SQLiteStore, string, error) {
	// Resolve local index path.
	if indexPath == "" {
		if cfg.LocalIndexPath != "" {
			indexPath = cfg.LocalIndexPath
		} else {
			indexPath = config.DefaultLocalIndexPath()
		}
	}

	// Open the local store (needed early for project resolution).
	ls, err := openLocalStore(indexPath)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("open local index: %w", err)
	}

	// Auto-resolve project from current directory if not specified.
	if project == "" {
		p, err := projectref.Resolve(ctx, ls, ".")
		if err != nil {
			ls.Close()
			return nil, nil, nil, "", fmt.Errorf("no project specified and no indexed project found in current directory" +
				" — use --project <path> or run from an indexed directory" +
				" (run: semidx index --project .)")
		}
		project = p.Name
	}

	// Build the chat client chain (Gemini primary, OpenRouter fallback).
	chatModel := model
	if chatModel == "" {
		chatModel = defaultChatModel
	}
	googleClient := chat.NewGoogleClient(cfg.GeminiBaseURL, cfg.GeminiAPIKey)
	providers := []chat.NamedClient{
		{Name: "gemini", Client: googleClient},
	}
	if cfg.OpenRouterAPIKey != "" {
		providers = append(providers, chat.NamedClient{
			Name:   "openrouter",
			Client: chat.NewOpenRouterClient(cfg.OpenRouterBaseURL, cfg.OpenRouterAPIKey),
		})
	}
	chatClient := chat.NewChain(providers...)
	chatClient.OnFallback = func(name string, err error) {
		fmt.Fprintf(os.Stderr, "[notice] %s unavailable (%v), trying next...\n", name, err)
	}

	// Build the embedding chain (shared with search.Service).
	emb := newEmbedder(cfg)

	// Build the search service and wrap it in an adapter.
	searchSvc := newSearchService(ls, emb)
	adapter := &searchAdapter{svc: searchSvc}

	// Resolve git worktree identity for correct scoping.
	var pipelineIdentity, pipelineWorktree string
	if gi := gitmeta.Resolve(ctx, "."); gi.IsGit {
		pipelineIdentity = gi.Identity
		pipelineWorktree = gi.Toplevel
	}

	// Build the RAG pipeline.
	pipeline := rag.NewPipeline(adapter, chatClient, rag.PipelineConfig{
		TopK:        5,
		MaxTokens:   4096,
		Temperature: 0.3,
		Model:       chatModel,
		Identity:    pipelineIdentity,
		Worktree:    pipelineWorktree,
	})

	// Build the fantasy-backed agent Runner when a chat LLM is configured.
	// Falls back to the rag.Pipeline (RAG mode) when runner is nil. Unlike the
	// old loop, the configured model is honored by the agent too. Read tools
	// plus the action tools under PolicyConfirm — the agent proposes an action
	// and the approve gate (a y/N prompt in the REPL) authorizes it before it
	// runs. Without an approver the action tools are omitted.
	var runner *agent.Runner
	if sel, ok := cfg.ResolveChatLLM(); ok {
		if model != "" { // an explicit --model flag overrides the configured chat model
			sel.Model = model
		}
		llmModel, err := llm.ResolveModel(ctx, llm.ProviderConfig{
			Type:    llm.ProviderType(sel.Provider),
			APIKey:  sel.APIKey,
			BaseURL: sel.BaseURL,
		}, sel.Model)
		if err != nil {
			slog.Warn("agent mode disabled: could not resolve chat model", "error", err)
		} else {
			resolver := agent.NewScopeResolver(ls)
			svc := search.NewService(ls, emb)
			tools := agent.ReadTools(svc, ls, resolver)
			if approve != nil {
				indexer := indexing.NewIndexer(ls, emb, 0, indexing.IndexerOpts{
					Logger:  slog.Default(),
					Workers: 2,
				})
				tools = append(tools, agent.ActionTools(ls, indexer, nil, agent.PolicyConfirm, approve)...)
			}
			temp := sel.Temperature
			runner = agent.NewRunner(llmModel, tools, agent.RunnerConfig{
				SystemPrompt: agent.SystemPrompt,
				Temperature:  &temp,
				// Bind the REPL session to the resolved project so the agent's
				// semantic_search stays in scope (the git identity/worktree also
				// drive worktree filtering, matching the RAG pipeline).
				Scope: agent.SearchScope{Project: project, Identity: pipelineIdentity, Worktree: pipelineWorktree},
			})
		}
	}

	return pipeline, runner, ls, project, nil
}
