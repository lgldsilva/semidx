package main

import (
	"context"

	"github.com/lgldsilva/semidx/internal/chat"
	"github.com/lgldsilva/semidx/internal/rag"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/internal/webadmin"
)

// buildAdminChatPipeline wires RAG chat for the admin SPA when at least one
// chat provider key is configured. Returns nil when chat is unavailable.
func (d *deps) buildAdminChatPipeline() webadmin.ChatPipeline {
	if d.cfg == nil || d.emb == nil {
		return nil
	}
	var providers []chat.NamedClient
	if d.cfg.GeminiAPIKey != "" {
		providers = append(providers, chat.NamedClient{
			Name:   "gemini",
			Client: chat.NewGoogleClient(d.cfg.GeminiBaseURL, d.cfg.GeminiAPIKey),
		})
	}
	if d.cfg.OpenRouterAPIKey != "" {
		providers = append(providers, chat.NamedClient{
			Name:   "openrouter",
			Client: chat.NewOpenRouterClient(d.cfg.OpenRouterBaseURL, d.cfg.OpenRouterAPIKey),
		})
	}
	if len(providers) == 0 {
		return nil
	}
	chatClient := chat.NewChain(providers...)
	svc := search.NewService(d.mustSearchStore(), d.emb)
	adapter := &adminSearchAdapter{svc: svc}
	return rag.NewPipeline(adapter, chatClient, rag.PipelineConfig{
		TopK:        8,
		MaxTokens:   4096,
		Temperature: 0.3,
		Model:       "gemini-2.0-flash",
	})
}

// mustSearchStore returns an IndexStore for search. During serve, database is
// already opened; fall back to nil-safe path via type assertion on store.Store.
func (d *deps) mustSearchStore() store.IndexStore {
	if d.db != nil {
		return d.db
	}
	// serve path always has d.db after database(); keep compile-safe.
	return d.db
}

// adminSearchAdapter implements rag.SearchService over search.Service.
type adminSearchAdapter struct {
	svc *search.Service
}

func (a *adminSearchAdapter) Search(ctx context.Context, req rag.SearchRequest) (*rag.SearchResponse, error) {
	resp, err := a.svc.Search(ctx, search.Request{
		Project:       req.Project,
		Identity:      req.Identity,
		Query:         req.Query,
		TopK:          req.TopK,
		KeywordOnly:   req.KeywordOnly,
		Worktree:      req.Worktree,
		Graph:         req.Graph,
		GraphMaxDepth: req.GraphMaxDepth,
	})
	if err != nil {
		return nil, err
	}
	results := make([]rag.SearchResult, len(resp.Results))
	for i, r := range resp.Results {
		results[i] = rag.SearchResult{
			FilePath: r.FilePath, Content: r.Content, Score: r.Score,
			StartLine: r.StartLine, EndLine: r.EndLine,
		}
	}
	return &rag.SearchResponse{Results: results, Fallback: resp.Fallback, Keyword: resp.Keyword}, nil
}
