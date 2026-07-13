package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/chat"
	"github.com/lgldsilva/semidx/internal/config"
	"github.com/lgldsilva/semidx/internal/embed"
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
	indexPath = resolveIndexPath(cfg, indexPath)
	ls, err := openLocalStore(indexPath)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("open local index: %w", err)
	}
	project, err = resolveProjectName(ctx, ls, project)
	if err != nil {
		ls.Close()
		return nil, nil, nil, "", err
	}

	chatModel := model
	if chatModel == "" {
		chatModel = defaultChatModel
	}
	chatClient := newChatRAGClient(cfg)
	emb := newEmbedder(cfg)
	adapter := &searchAdapter{svc: newSearchService(ls, emb)}

	pipelineIdentity, pipelineWorktree := gitScope(ctx)
	pipeline := rag.NewPipeline(adapter, chatClient, rag.PipelineConfig{
		TopK: 5, MaxTokens: 4096, Temperature: 0.3, Model: chatModel,
		Identity: pipelineIdentity, Worktree: pipelineWorktree,
	})
	runner := buildChatRAGRunner(ctx, chatRunnerOpts{
		cfg: cfg, ls: ls, emb: emb, project: project, model: model,
		identity: pipelineIdentity, worktree: pipelineWorktree, approve: approve,
	})
	return pipeline, runner, ls, project, nil
}

func resolveIndexPath(cfg *config.Config, indexPath string) string {
	if indexPath != "" {
		return indexPath
	}
	if cfg.LocalIndexPath != "" {
		return cfg.LocalIndexPath
	}
	return config.DefaultLocalIndexPath()
}

func resolveProjectName(ctx context.Context, ls *localstore.SQLiteStore, project string) (string, error) {
	if project != "" {
		return project, nil
	}
	p, err := projectref.Resolve(ctx, ls, ".")
	if err != nil {
		return "", fmt.Errorf("no project specified and no indexed project found in current directory" +
			" — use --project <path> or run from an indexed directory" +
			" (run: semidx index --project .)")
	}
	return p.Name, nil
}

func newChatRAGClient(cfg *config.Config) *chat.ChainClient {
	providers := []chat.NamedClient{{Name: "gemini", Client: chat.NewGoogleClient(cfg.GeminiBaseURL, cfg.GeminiAPIKey)}}
	if cfg.OpenRouterAPIKey != "" {
		providers = append(providers, chat.NamedClient{
			Name: "openrouter", Client: chat.NewOpenRouterClient(cfg.OpenRouterBaseURL, cfg.OpenRouterAPIKey),
		})
	}
	chain := chat.NewChain(providers...)
	chain.OnFallback = func(name string, err error) {
		fmt.Fprintf(os.Stderr, "[notice] %s unavailable (%v), trying next...\n", name, err)
	}
	return chain
}

func gitScope(ctx context.Context) (identity, worktree string) {
	if gi := gitmeta.Resolve(ctx, "."); gi.IsGit {
		return gi.Identity, gi.Toplevel
	}
	return "", ""
}

type chatRunnerOpts struct {
	cfg      *config.Config
	ls       *localstore.SQLiteStore
	emb      embed.Embedder
	project  string
	model    string
	identity string
	worktree string
	approve  permission.Approver
}

func buildChatRAGRunner(ctx context.Context, o chatRunnerOpts) *agent.Runner {
	sel, ok := o.cfg.ResolveChatLLM()
	if !ok {
		return nil
	}
	if o.model != "" {
		sel.Model = o.model
	}
	llmModel, err := llm.ResolveModel(ctx, llm.ProviderConfig{
		Type: llm.ProviderType(sel.Provider), APIKey: sel.APIKey, BaseURL: sel.BaseURL,
	}, sel.Model)
	if err != nil {
		slog.Warn("agent mode disabled: could not resolve chat model", "error", err)
		return nil
	}
	svc := search.NewService(o.ls, o.emb)
	tools := agent.ReadTools(svc, o.ls, agent.NewScopeResolver(o.ls))
	if o.approve != nil {
		indexer := indexing.NewIndexer(o.ls, o.emb, 0, indexing.IndexerOpts{Logger: slog.Default(), Workers: 2})
		tools = append(tools, agent.ActionTools(o.ls, indexer, nil, agent.PolicyConfirm, o.approve)...)
	}
	temp := sel.Temperature
	return agent.NewRunner(llmModel, tools, agent.RunnerConfig{
		SystemPrompt: agent.SystemPrompt,
		Temperature:  &temp,
		Scope:        agent.SearchScope{Project: o.project, Identity: o.identity, Worktree: o.worktree},
	})
}
