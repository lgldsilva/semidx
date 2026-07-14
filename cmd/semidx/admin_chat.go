package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"charm.land/fantasy"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/chat"
	"github.com/lgldsilva/semidx/internal/config"
	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/llm"
	"github.com/lgldsilva/semidx/internal/rag"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/internal/webadmin"
)

// Admin chat modes (the GET /admin/api/chat/config contract): "agent" runs the
// tool-calling loop, "rag" runs deterministic server-side retrieval
// (rag.FantasyPipeline) over a tool-less Runner.
const (
	chatModeAgent = "agent"
	chatModeRAG   = "rag"
)

// buildAdminChatPipeline wires the admin SPA chat as a PER-REQUEST factory
// (adminChat): each turn picks its mode/model from the request options. It
// enables for ANY configured chat provider (google/anthropic/openrouter/groq/
// openai-compatible/copilot) — the enablement decision is Config.ResolveChatLLM;
// nil when chat is unavailable (the SPA endpoints then answer 503).
//
// Edge case (was papered over by a legacy non-agent chat.Chain fallback, now
// removed): a bare OPENROUTER_API_KEY (or GROQ_API_KEY) without
// SEMIDX_CHAT_MODEL does NOT resolve a chat provider — those providers have no
// default model — so chat comes up disabled and the SPA gets the 503 whose
// message must guide the user to set a provider key AND SEMIDX_CHAT_MODEL.
func (d *deps) buildAdminChatPipeline() webadmin.ChatPipeline {
	if d.cfg == nil || d.emb == nil {
		return nil
	}
	sel, ok := d.cfg.ResolveChatLLM()
	if !ok {
		return nil
	}
	svc := search.NewService(d.mustSearchStore(), d.emb)
	idxStore := d.mustSearchStore()
	// repo tools only when project paths are local; nil resolver omits them.
	var resolver agent.ScopeResolver
	if d.db != nil {
		resolver = agent.NewScopeResolver(idxStore)
	}
	// Agent-mode tools are model-independent: build them once and share them
	// across every per-request Runner.
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

	p := &adminChat{
		d:      d,
		def:    sel,
		models: d.cfg.ChatModelAllowlist(),
		svc:    svc,
		tools:  tools,
		cache:  map[string]fantasy.LanguageModel{},
	}
	// Resolve the default model eagerly so a misconfigured provider disables
	// chat at startup (503 with guidance) instead of failing every request.
	if _, err := p.modelFor(context.Background(), ""); err != nil {
		slog.Warn("admin chat: could not resolve chat model", "provider", sel.Provider, "error", err)
		return nil
	}
	return p
}

// chatBackend is the per-turn pipeline surface both modes share, implemented
// by agentChatPipeline (mode "agent") and rag.FantasyPipeline (mode "rag").
type chatBackend interface {
	Ask(ctx context.Context, question, project string, history []chat.Message) (*rag.Answer, error)
	StreamAsk(ctx context.Context, question, project string, history []chat.Message) (<-chan chat.StreamChunk, []rag.Source, string, bool, error)
}

// adminChat implements webadmin.ChatPipeline as a per-request factory: each
// turn builds its backend for the requested mode/model (validated upstream by
// the webadmin handlers against Config()). Resolved fantasy.LanguageModels are
// cached per provider/model id under mu, so repeated turns reuse the provider
// while agent.NewRunner (cheap) runs per turn.
type adminChat struct {
	d      *deps
	def    config.ChatLLM
	models []config.ChatModelSpec
	svc    *search.Service
	tools  []fantasy.AgentTool

	mu    sync.Mutex
	cache map[string]fantasy.LanguageModel
}

// Config reports the frozen GET /admin/api/chat/config contract.
func (p *adminChat) Config() webadmin.ChatConfig {
	models := make([]webadmin.ChatModelInfo, 0, len(p.models))
	for _, m := range p.models {
		models = append(models, webadmin.ChatModelInfo{ID: m.Model, Provider: m.Provider, Default: m.Default})
	}
	actions := "off"
	if pol, ok := agent.ParseActionPolicy(p.d.cfg.AgentActions); ok {
		actions = pol.String()
	}
	return webadmin.ChatConfig{
		Enabled:      true,
		Modes:        []string{chatModeAgent, chatModeRAG},
		DefaultMode:  chatModeAgent,
		DefaultModel: p.def.Model,
		Models:       models,
		AgentActions: actions,
	}
}

func (p *adminChat) Ask(ctx context.Context, question, project string, history []chat.Message, opts webadmin.ChatOptions) (*rag.Answer, error) {
	be, err := p.backendFor(ctx, opts)
	if err != nil {
		return nil, err
	}
	return be.Ask(ctx, question, project, history)
}

func (p *adminChat) StreamAsk(ctx context.Context, question, project string, history []chat.Message, opts webadmin.ChatOptions) (<-chan chat.StreamChunk, []rag.Source, string, bool, error) {
	be, err := p.backendFor(ctx, opts)
	if err != nil {
		return nil, nil, "", false, err
	}
	return be.StreamAsk(ctx, question, project, history)
}

// backendFor builds the turn's pipeline: mode "rag" wraps a tool-less Runner in
// the FantasyPipeline (retrieval is server-side); mode "agent" (the default)
// gives the Runner the shared tool set and system prompt.
func (p *adminChat) backendFor(ctx context.Context, opts webadmin.ChatOptions) (chatBackend, error) {
	model, err := p.modelFor(ctx, opts.Model)
	if err != nil {
		return nil, err
	}
	temp := p.def.Temperature
	switch opts.Mode {
	case chatModeRAG:
		// No tools and no agent system prompt: the FantasyPipeline injects its
		// own RAG instructions + assembled context as the system message.
		runner := agent.NewRunner(model, nil, agent.RunnerConfig{Temperature: &temp})
		return rag.NewFantasyPipeline(&adminSearchAdapter{svc: p.svc}, runner, rag.PipelineConfig{TopK: 8}), nil
	case "", chatModeAgent:
		runner := agent.NewRunner(model, p.tools, agent.RunnerConfig{
			SystemPrompt: agent.SystemPrompt,
			Temperature:  &temp,
		})
		return &agentChatPipeline{runner: runner}, nil
	default:
		// The webadmin handler already 400s unknown modes; keep a defensive error.
		return nil, fmt.Errorf("unknown chat mode %q", opts.Mode)
	}
}

// modelFor returns the fantasy model for id, resolving and caching it on first
// use. An empty id (or the default's id) means the server's default chat model.
func (p *adminChat) modelFor(ctx context.Context, id string) (fantasy.LanguageModel, error) {
	sel := p.def
	if id != "" && id != p.def.Model {
		spec, ok := p.specFor(id)
		if !ok {
			return nil, fmt.Errorf("model %q is not in the chat allowlist", id)
		}
		s, ok := p.d.chatLLMFor(spec)
		if !ok {
			return nil, fmt.Errorf("no usable provider credentials for chat model %q", id)
		}
		sel = s
	}
	key := sel.Provider + "/" + sel.Model
	p.mu.Lock()
	defer p.mu.Unlock()
	if m, ok := p.cache[key]; ok {
		return m, nil
	}
	m, err := llm.ResolveModel(ctx, llm.ProviderConfig{
		Type:    llm.ProviderType(sel.Provider),
		APIKey:  sel.APIKey,
		BaseURL: sel.BaseURL,
	}, sel.Model)
	if err != nil {
		return nil, err
	}
	p.cache[key] = m
	return m, nil
}

// specFor finds an allowlist entry by model id.
func (p *adminChat) specFor(id string) (config.ChatModelSpec, bool) {
	for _, m := range p.models {
		if m.Model == id {
			return m, true
		}
	}
	return config.ChatModelSpec{}, false
}

// chatLLMFor resolves provider credentials for one allowlist entry, reusing
// config's explicit-provider fallback logic (embedding keys, provider default
// base URLs). The explicit SEMIDX_CHAT_API_KEY/BASE_URL pair belongs to the
// configured default provider only — it must not leak onto other providers.
// Unlike config.usable() (which tolerates keyless openai-compatible local
// servers), an allowlist entry for any OTHER provider without a key is
// rejected here, so a misconfigured entry fails fast instead of surfacing a
// provider 401 mid-chat.
func (d *deps) chatLLMFor(spec config.ChatModelSpec) (config.ChatLLM, bool) {
	c := d.cfg.Clone()
	if spec.Provider != c.ChatProvider {
		c.ChatAPIKey = ""
		c.ChatBaseURL = ""
	}
	c.ChatProvider = spec.Provider
	c.ChatModel = spec.Model
	sel, ok := c.ResolveChatLLM()
	if !ok {
		return sel, false
	}
	if sel.APIKey == "" && sel.Provider != string(llm.ProviderOpenAICompat) {
		return sel, false
	}
	return sel, true
}

// agentChatPipeline drives the admin chat through the fantasy agent Runner.
// It implements webadmin.ChatPipeline.
type agentChatPipeline struct {
	runner *agent.Runner
}

// scopeForProject maps a chat's project to the agent search scope: a named
// project pins the turn to it; an empty project is the global chat, which fans
// semantic_search across every indexed project.
func scopeForProject(project string) agent.SearchScope {
	if project == "" {
		return agent.SearchScope{All: true}
	}
	return agent.SearchScope{Project: project}
}

func (a *agentChatPipeline) Ask(ctx context.Context, question, project string, history []chat.Message) (*rag.Answer, error) {
	// Bind the turn to the requested project so semantic_search stays in scope
	// (the model can't wander to another project). Contract, not a prompt hint.
	// An empty project means the global chat: search across ALL projects.
	ctx = agent.ContextWithScope(ctx, scopeForProject(project))
	msgs := a.runner.CompactHistory(ctx, agent.MessagesFromChat(history))
	answer, err := a.runner.Ask(ctx, question, msgs)
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
			Content: h.Content, Score: h.Score, Keyword: h.Keyword, Project: h.Project,
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
			Content: h.Content, Score: h.Score, Keyword: h.Keyword, Project: h.Project,
		})
	}
	return out
}

// Stream sanitization bounds: tool-call argument string values and tool-result
// previews are truncated before leaving the server (frozen SSE contract caps
// the preview at 500 runes).
const (
	toolArgValueMaxRunes = 200
	toolPreviewMaxRunes  = 500
)

// chatBackendErrMsg is the only error text the stream ever shows a client. The
// real error goes to slog: provider failures can embed base URLs or keys.
const chatBackendErrMsg = "chat backend failed — check server logs"

func (a *agentChatPipeline) StreamAsk(ctx context.Context, question, project string, history []chat.Message) (<-chan chat.StreamChunk, []rag.Source, string, bool, error) {
	// Real streaming: the agent loop runs in a goroutine and each assistant
	// text delta is pushed onto the channel as it arrives (replacing the old
	// single-chunk "fake stream"). The model name is known up front, so the SSE
	// layer can emit it in the leading sources event before tokens flow.
	model := a.runner.Model()
	ch := make(chan chat.StreamChunk, 16)
	// Bind the stream turn to the project (contract for semantic_search scope);
	// an empty project is the global chat (search across ALL projects).
	ctx = agent.ContextWithScope(ctx, scopeForProject(project))
	msgs := a.runner.CompactHistory(ctx, agent.MessagesFromChat(history))
	go func() {
		defer close(ch)
		send := func(c chat.StreamChunk) {
			select {
			case ch <- c:
			case <-ctx.Done():
			}
		}
		// Per-call start times keyed by tool-call id: parallel tools interleave,
		// and the callbacks may run on concurrent tool goroutines.
		var (
			toolMu     sync.Mutex
			toolStarts = map[string]time.Time{}
		)
		cb := agent.StreamCallbacks{
			OnText: func(delta string) { send(chat.StreamChunk{Content: delta}) },
			OnToolCall: func(id, name, input string) {
				toolMu.Lock()
				toolStarts[id] = time.Now()
				toolMu.Unlock()
				send(chat.StreamChunk{Tool: &chat.ToolEvent{
					Kind: chat.ToolEventCall, ID: id, Name: name,
					Args: agent.SanitizeToolArgs(input, toolArgValueMaxRunes),
				}})
			},
			OnToolResult: func(id, name, result string, isError bool) {
				var elapsed int64
				toolMu.Lock()
				if t0, ok := toolStarts[id]; ok {
					elapsed = time.Since(t0).Milliseconds()
					delete(toolStarts, id)
				}
				toolMu.Unlock()
				preview, truncated := agent.PreviewToolResult(result, toolPreviewMaxRunes)
				send(chat.StreamChunk{Tool: &chat.ToolEvent{
					Kind: chat.ToolEventResult, ID: id, Name: name,
					Preview: preview, IsError: isError,
					ElapsedMS: elapsed, Truncated: truncated,
				}})
			},
		}
		done := chat.StreamChunk{Done: true, Model: model}
		answer, err := a.runner.Stream(ctx, question, msgs, cb)
		switch {
		case err != nil:
			// The SSE headers are already sent by the time tokens stream, so we
			// can't fail the request — log the real error and surface only a
			// generic message. A canceled request is the client going away, not
			// a backend failure: terminate silently.
			slog.Error("admin agent stream failed", "error", err, "project", project)
			if ctx.Err() == nil {
				done.Err = chatBackendErrMsg
			}
		case answer != nil:
			// Deliver citations on the terminal chunk (known only after tool calls).
			hits, fb := agent.SourcesFromTrace(answer.Trace)
			done.Sources = chatSources(hits)
			done.Fallback = fb
		}
		send(done)
	}()
	return ch, nil, model, false, nil
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
