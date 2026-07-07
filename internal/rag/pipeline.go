package rag

import (
	"context"
	"fmt"

	"github.com/lgldsilva/semidx/internal/chat"
)

// Pipeline runs the RAG loop: search → assemble context → call LLM.
type Pipeline struct {
	search SearchService
	chat   chat.Client
	config PipelineConfig
}

// NewPipeline creates a RAG pipeline.
func NewPipeline(search SearchService, chatClient chat.Client, config PipelineConfig) *Pipeline {
	if config.TopK <= 0 {
		config.TopK = 5
	}
	if config.MaxTokens <= 0 {
		config.MaxTokens = 4096
	}
	return &Pipeline{
		search: search,
		chat:   chatClient,
		config: config,
	}
}

// Ask runs one RAG turn: retrieves chunks from the index, builds context, and
// sends the prompt to the chat LLM. history is the previous conversation turns
// (may be nil/empty for single-turn queries).
func (p *Pipeline) Ask(ctx context.Context, question, project string, history []chat.Message) (*Answer, error) {
	// 1. Retrieve relevant chunks.
	searchResp, err := p.search.Search(ctx, SearchRequest{
		Project:  project,
		Query:    question,
		TopK:     p.config.TopK,
		Identity: p.config.Identity,
		Worktree: p.config.Worktree,
	})
	if err != nil {
		return nil, fmt.Errorf("search failed: %w — is the project indexed? Run: semidx index --project <path>", err)
	}

	// 2. Convert results to sources.
	sources := make([]Source, len(searchResp.Results))
	for i, r := range searchResp.Results {
		sources[i] = Source{
			File:      r.FilePath,
			StartLine: r.StartLine,
			EndLine:   r.EndLine,
			Content:   r.Content,
			Score:     r.Score,
		}
	}

	// 3. Build context string with token-budget awareness.
	contextStr := assembleContext(sources, 8000)

	// 4. Assemble messages for the LLM.
	messages := assemblePrompt(question, contextStr, history)

	// 5. Call the chat LLM.
	resp, err := p.chat.SendMessage(ctx, chat.Request{
		Messages:    messages,
		Temperature: p.config.Temperature,
		MaxTokens:   p.config.MaxTokens,
		Model:       p.config.Model,
	})
	if err != nil {
		return nil, fmt.Errorf("chat failed: %w", err)
	}

	// 6. Return the answer.
	return &Answer{
		Content:  resp.Content,
		Sources:  sources,
		Model:    resp.Model,
		Fallback: searchResp.Fallback,
		Keyword:  searchResp.Keyword,
	}, nil
}

// StreamAsk runs one RAG turn with a streaming response: search, build context,
// then stream the LLM output. Returns the chunk channel, sources, model name,
// search-fallback flag, and any connection-level error.
func (p *Pipeline) StreamAsk(ctx context.Context, question, project string, history []chat.Message) (<-chan chat.StreamChunk, []Source, string, bool, error) {
	// 1. Retrieve relevant chunks (synchronous).
	searchResp, err := p.search.Search(ctx, SearchRequest{
		Project:  project,
		Query:    question,
		TopK:     p.config.TopK,
		Identity: p.config.Identity,
		Worktree: p.config.Worktree,
	})
	if err != nil {
		return nil, nil, "", false, fmt.Errorf("search failed: %w — is the project indexed? Run: semidx index --project <path>", err)
	}

	// 2. Convert results to sources.
	sources := make([]Source, len(searchResp.Results))
	for i, r := range searchResp.Results {
		sources[i] = Source{
			File:      r.FilePath,
			StartLine: r.StartLine,
			EndLine:   r.EndLine,
			Content:   r.Content,
			Score:     r.Score,
		}
	}

	// 3. Build context string.
	contextStr := assembleContext(sources, 8000)

	// 4. Assemble messages for the LLM.
	messages := assemblePrompt(question, contextStr, history)

	// 5. Try streaming, fall back to non-streaming if the client doesn't
	// support the StreamClient interface.
	sc, ok := p.chat.(chat.StreamClient)
	if ok {
		chunks, err := sc.StreamMessage(ctx, chat.Request{
			Messages:    messages,
			Temperature: p.config.Temperature,
			MaxTokens:   p.config.MaxTokens,
			Model:       p.config.Model,
		})
		if err == nil {
			return chunks, sources, p.config.Model, searchResp.Fallback, nil
		}
		// Stream init failed; fall through to non-streaming.
	}

	// 6. Non-streaming fallback.
	resp, err := p.chat.SendMessage(ctx, chat.Request{
		Messages:    messages,
		Temperature: p.config.Temperature,
		MaxTokens:   p.config.MaxTokens,
		Model:       p.config.Model,
	})
	if err != nil {
		return nil, nil, "", false, fmt.Errorf("chat failed: %w", err)
	}

	model := resp.Model
	if model == "" {
		model = p.config.Model
	}

	ch := make(chan chat.StreamChunk, 2)
	ch <- chat.StreamChunk{Content: resp.Content}
	ch <- chat.StreamChunk{Done: true, Model: model}
	close(ch)
	return ch, sources, model, searchResp.Fallback, nil
}
