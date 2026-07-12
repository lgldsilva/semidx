package main

import (
	"context"
	"fmt"
	"os"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/chat"
	"github.com/lgldsilva/semidx/internal/config"
	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/localstore"
	"github.com/lgldsilva/semidx/internal/projectref"
	"github.com/lgldsilva/semidx/internal/rag"
	"github.com/lgldsilva/semidx/internal/search"
)

// buildPipeline constructs the full RAG pipeline and its dependencies.
// Caller must close the returned SQLiteStore when done.
// Returns the pipeline, optional agent (nil when tool calling unavailable), store, and resolved project name.
func buildPipeline(ctx context.Context, cfg *config.Config, indexPath, project, model string) (*rag.Pipeline, *agent.Agent, *localstore.SQLiteStore, string, error) {
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

	// Build agent if tool calling is available (Gemini >=2.0 supports tools).
	// Fall back to rag.Pipeline when agent is nil.
	var agentInstance *agent.Agent
	if cfg.GeminiAPIKey != "" {
		resolver := agent.NewScopeResolver(ls)
		svc := search.NewService(ls, emb)

		tools := []agent.Tool{
			agent.NewSearchTool(svc),
			agent.NewIndexStatusTool(ls),
			agent.NewListProjectsTool(ls),
		}
		// repotools need a path resolver; only add when the resolver is available.
		tools = append(tools,
			agent.NewRepoWorktreesTool(resolver),
			agent.NewRepoBranchesTool(resolver),
			agent.NewRepoStatusTool(resolver),
		)

		agentInstance = agent.NewAgent(chatClient, tools, resolver)
	}

	return pipeline, agentInstance, ls, project, nil
}
