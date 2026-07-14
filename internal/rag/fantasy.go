package rag

import (
	"context"
	"fmt"
	"log/slog"

	"charm.land/fantasy"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/chat"
)

// ChatRunner is the minimal tool-less agent surface the FantasyPipeline drives.
// *agent.Runner implements it: built with no tools, the fantasy loop degrades
// to a plain single-step chat call. Tests substitute a fake.
type ChatRunner interface {
	Ask(ctx context.Context, question string, history []fantasy.Message) (*agent.Answer, error)
	Stream(ctx context.Context, question string, history []fantasy.Message, cb agent.StreamCallbacks) (*agent.Answer, error)
	CompactHistory(ctx context.Context, history []fantasy.Message) []fantasy.Message
	Model() string
}

// fantasyBackendErrMsg is the only error text a fantasy RAG stream ever shows a
// client — provider failures can embed base URLs or keys, so the real error
// goes to slog only (same contract as the agent chat stream).
const fantasyBackendErrMsg = "chat backend failed — check server logs"

// FantasyPipeline runs the RAG loop on the fantasy stack: semantic search →
// diversify/assembleContext (the exact shaping Pipeline applies) → one
// tool-less ChatRunner turn grounded in the assembled context. It is the "rag"
// chat mode: retrieval is deterministic and server-side, unlike the "agent"
// mode where the model decides when (and whether) to call semantic_search.
type FantasyPipeline struct {
	search SearchService
	runner ChatRunner
	config PipelineConfig
}

// NewFantasyPipeline builds the fantasy-backed RAG pipeline. TopK defaults to 5
// (mirroring NewPipeline). MaxTokens/Temperature/Model in config are unused —
// they belong to the runner's model, configured by the caller.
func NewFantasyPipeline(search SearchService, runner ChatRunner, config PipelineConfig) *FantasyPipeline {
	if config.TopK <= 0 {
		config.TopK = 5
	}
	return &FantasyPipeline{search: search, runner: runner, config: config}
}

// Ask runs one RAG turn: retrieve → assemble context → tool-less runner call.
func (p *FantasyPipeline) Ask(ctx context.Context, question, project string, history []chat.Message) (*Answer, error) {
	sources, contextStr, resp, err := p.retrieve(ctx, question, project)
	if err != nil {
		return nil, err
	}
	ans, err := p.runner.Ask(ctx, question, p.messages(ctx, contextStr, history))
	if err != nil {
		return nil, fmt.Errorf("chat failed: %w", err)
	}
	return &Answer{
		Content:  ans.Content,
		Sources:  sources,
		Model:    ans.Model,
		Fallback: resp.Fallback,
		Keyword:  resp.Keyword,
	}, nil
}

// StreamAsk runs one RAG turn with a streamed answer. Retrieval is synchronous,
// so sources and the model name are returned up front (the SSE layer emits them
// before tokens flow); text deltas are pushed onto the channel as they arrive.
func (p *FantasyPipeline) StreamAsk(ctx context.Context, question, project string, history []chat.Message) (<-chan chat.StreamChunk, []Source, string, bool, error) {
	sources, contextStr, resp, err := p.retrieve(ctx, question, project)
	if err != nil {
		return nil, nil, "", false, err
	}
	msgs := p.messages(ctx, contextStr, history)
	model := p.runner.Model()
	ch := make(chan chat.StreamChunk, 16)
	go func() {
		defer close(ch)
		send := func(c chat.StreamChunk) {
			select {
			case ch <- c:
			case <-ctx.Done():
			}
		}
		cb := agent.StreamCallbacks{
			OnText: func(delta string) { send(chat.StreamChunk{Content: delta}) },
		}
		done := chat.StreamChunk{Done: true, Model: model}
		if _, err := p.runner.Stream(ctx, question, msgs, cb); err != nil {
			// The SSE headers are already sent by the time tokens stream, so the
			// request can't fail — log the real error and surface only a generic
			// message. A canceled request is the client going away, not a
			// backend failure: terminate silently.
			slog.Error("rag fantasy stream failed", "error", err, "project", project)
			if ctx.Err() == nil {
				done.Err = fantasyBackendErrMsg
			}
		}
		send(done)
	}()
	return ch, sources, model, resp.Fallback, nil
}

// retrieve runs the search and shapes the sources + context string exactly like
// Pipeline: sensitive-path filter, per-file/per-project diversity caps, and the
// token-budgeted context assembly.
func (p *FantasyPipeline) retrieve(ctx context.Context, question, project string) ([]Source, string, *SearchResponse, error) {
	resp, err := p.search.Search(ctx, SearchRequest{
		Project:       project,
		Query:         question,
		TopK:          p.config.TopK,
		Identity:      p.config.Identity,
		Worktree:      p.config.Worktree,
		Graph:         p.config.Graph,
		GraphMaxDepth: p.config.GraphMaxDepth,
		PathPrefix:    p.config.PathPrefix,
	})
	if err != nil {
		return nil, "", nil, fmt.Errorf("search failed: %w — is the project indexed? Run: semidx index --project <path>", err)
	}
	sources := filterSensitiveSources(resp.Results)
	sources = diversify(sources, 3, 15) // same caps as Pipeline
	return sources, assembleContext(sources, 8000), resp, nil
}

// messages assembles the runner history for one turn: a single system message
// (instructions + context) followed by the compacted conversation history. The
// user question itself travels through the runner's prompt argument. The system
// message is prepended AFTER compaction so a fresh RAG context is never folded
// into a history summary.
func (p *FantasyPipeline) messages(ctx context.Context, contextStr string, history []chat.Message) []fantasy.Message {
	hist := p.runner.CompactHistory(ctx, agent.MessagesFromChat(history))
	msgs := make([]fantasy.Message, 0, 1+len(hist))
	msgs = append(msgs, fantasy.NewSystemMessage(systemContent(contextStr)))
	return append(msgs, hist...)
}
