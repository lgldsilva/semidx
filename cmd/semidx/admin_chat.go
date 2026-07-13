package main

import (
	"context"
	"fmt"
	"log/slog"

	"charm.land/fantasy"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/chat"
	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/llm"
	"github.com/lgldsilva/semidx/internal/rag"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/internal/webadmin"
)

// buildAdminChatPipeline wires the admin SPA chat. It uses the fantasy agent
// Runner for ANY configured chat provider (google/anthropic/openrouter/groq/
// openai-compatible) — the enablement decision is Config.ResolveChatLLM, not a
// Gemini/OpenRouter key. Falls back to the legacy non-agent RAG chain only when
// no chat provider resolves but a base key exists; nil when chat is unavailable.
func (d *deps) buildAdminChatPipeline() webadmin.ChatPipeline {
	if d.cfg == nil || d.emb == nil {
		return nil
	}
	svc := search.NewService(d.mustSearchStore(), d.emb)

	// Primary path: the agent Runner for whatever provider ResolveChatLLM picks
	// (explicit SEMIDX_CHAT_* override, or auto-detected). The tool loop calls
	// semantic_search / repo_* before answering.
	if sel, ok := d.cfg.ResolveChatLLM(); ok {
		model, err := llm.ResolveModel(context.Background(), llm.ProviderConfig{
			Type:    llm.ProviderType(sel.Provider),
			APIKey:  sel.APIKey,
			BaseURL: sel.BaseURL,
		}, sel.Model)
		if err != nil {
			slog.Warn("admin chat: could not resolve chat model", "provider", sel.Provider, "error", err)
		} else {
			idxStore := d.mustSearchStore()
			// repo tools only when project paths are local; nil resolver omits them.
			var resolver agent.ScopeResolver
			if d.db != nil {
				resolver = agent.NewScopeResolver(idxStore)
			}
			tools := agent.ReadTools(svc, idxStore, resolver)
			// Action tools are opt-in via SEMIDX_AGENT_ACTIONS (propose/execute);
			// the path guard still bounds writes to registered project trees.
			if pol, ok := agent.ParseActionPolicy(d.cfg.AgentActions); ok {
				indexer := indexing.NewIndexer(idxStore, d.emb, 0, indexing.IndexerOpts{
					Logger:  slog.Default(),
					Workers: 2,
				})
				tools = append(tools, agent.ActionTools(idxStore, indexer, nil, pol, nil)...)
			}
			// External MCP servers (SEMIDX_MCP_CLIENT_CONFIG): the agent uses their
			// tools as a client. No-op when unconfigured; failures are logged.
			tools = append(tools, d.mcpClientTools(context.Background())...)
			temp := sel.Temperature
			runner := agent.NewRunner(model, tools, agent.RunnerConfig{
				SystemPrompt: agent.SystemPrompt,
				Temperature:  &temp,
			})
			return &agentChatPipeline{runner: runner}
		}
	}

	// Fallback: legacy non-agent RAG chain, only if a base provider key exists.
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
	adapter := &adminSearchAdapter{svc: svc}
	return rag.NewPipeline(adapter, chat.NewChain(providers...), rag.PipelineConfig{
		TopK: 8, MaxTokens: 4096, Temperature: 0.3, Model: "gemini-2.0-flash",
	})
}

// agentChatPipeline drives the admin chat through the fantasy agent Runner.
// It implements webadmin.ChatPipeline.
type agentChatPipeline struct {
	runner *agent.Runner
}

func (a *agentChatPipeline) Ask(ctx context.Context, question, project string, history []chat.Message) (*rag.Answer, error) {
	// Bind the turn to the requested project so semantic_search stays in scope
	// (the model can't wander to another project). Contract, not a prompt hint.
	ctx = agent.ContextWithScope(ctx, agent.SearchScope{Project: project})
	answer, err := a.runner.Ask(ctx, question, adminHistoryToMessages(history))
	if err != nil {
		return nil, fmt.Errorf("agent ask failed: %w", err)
	}
	// Surface the real semantic_search results as citations (and the fallback
	// flag) instead of discarding the trace.
	hits, fallback := agent.SourcesFromTrace(answer.Trace)
	return &rag.Answer{
		Content:  answer.Content,
		Model:    answer.Model,
		Sources:  ragSources(hits),
		Fallback: fallback,
	}, nil
}

// ragSources maps agent search hits to rag.Source (admin non-stream answer).
func ragSources(hits []agent.SearchHit) []rag.Source {
	out := make([]rag.Source, 0, len(hits))
	for _, h := range hits {
		out = append(out, rag.Source{
			File: h.File, StartLine: h.StartLine, EndLine: h.EndLine,
			Content: h.Content, Score: h.Score, Keyword: h.Keyword,
		})
	}
	return out
}

// chatSources maps agent search hits to chat.Source (carried on the terminal
// stream chunk so the SSE layer can emit citations after the tool calls).
func chatSources(hits []agent.SearchHit) []chat.Source {
	out := make([]chat.Source, 0, len(hits))
	for _, h := range hits {
		out = append(out, chat.Source{
			File: h.File, StartLine: h.StartLine, EndLine: h.EndLine,
			Content: h.Content, Score: h.Score, Keyword: h.Keyword,
		})
	}
	return out
}

func (a *agentChatPipeline) StreamAsk(ctx context.Context, question, project string, history []chat.Message) (<-chan chat.StreamChunk, []rag.Source, string, bool, error) {
	// Real streaming: the agent loop runs in a goroutine and each assistant
	// text delta is pushed onto the channel as it arrives (replacing the old
	// single-chunk "fake stream"). The model name is known up front, so the SSE
	// layer can emit it in the leading sources event before tokens flow.
	model := a.runner.Model()
	ch := make(chan chat.StreamChunk, 16)
	msgs := adminHistoryToMessages(history)
	// Bind the stream turn to the project (contract for semantic_search scope).
	ctx = agent.ContextWithScope(ctx, agent.SearchScope{Project: project})
	go func() {
		defer close(ch)
		cb := agent.StreamCallbacks{
			OnText: func(delta string) {
				select {
				case ch <- chat.StreamChunk{Content: delta}:
				case <-ctx.Done():
				}
			},
		}
		done := chat.StreamChunk{Done: true, Model: model}
		answer, err := a.runner.Stream(ctx, question, msgs, cb)
		if err != nil {
			// The channel contract has no error field and the SSE headers are
			// already sent by the time tokens stream, so we can't fail the
			// request here — log and terminate the stream cleanly.
			slog.Error("admin agent stream failed", "error", err, "project", project)
		} else if answer != nil {
			// Deliver citations on the terminal chunk (known only after tool calls).
			hits, fb := agent.SourcesFromTrace(answer.Trace)
			done.Sources = chatSources(hits)
			done.Fallback = fb
		}
		select {
		case ch <- done:
		case <-ctx.Done():
		}
	}()
	return ch, nil, model, false, nil
}

// adminHistoryToMessages converts chat.History turns (text-only) into fantasy
// messages for the Runner. Full tool_call history is Phase 5.
func adminHistoryToMessages(msgs []chat.Message) []fantasy.Message {
	out := make([]fantasy.Message, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "user":
			out = append(out, fantasy.NewUserMessage(m.Content))
		case "system":
			out = append(out, fantasy.NewSystemMessage(m.Content))
		case "assistant":
			out = append(out, fantasy.Message{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: m.Content}},
			})
		}
	}
	return out
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
