// Package search unifies the semantic-search flow shared by the CLI (search and
// sgrep) and the MCP server, which previously each reimplemented it. A single
// Service resolves the model, embeds the query, runs the vector search, and
// falls back to keyword search when embeddings are unavailable; Formatters then
// render the same Response in different output styles.
package search

import (
	"context"
	"fmt"

	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/store"
)

// Service runs semantic searches against an IndexStore using an Embedder.
type Service struct {
	store store.IndexStore
	emb   embed.Embedder
}

// NewService wires a search Service.
func NewService(s store.IndexStore, e embed.Embedder) *Service {
	return &Service{store: s, emb: e}
}

// Request describes one search.
type Request struct {
	Project string
	Query   string
	Model   string // optional; overrides the project's stored model
	TopK    int    // <= 0 defaults to 5
	// KeywordOnly forces keyword search with no embedding (used when the index
	// was built without a model). It is not a fallback, so Fallback stays false.
	KeywordOnly bool
	// Worktree, when set, restricts results to the file versions that worktree
	// currently has checked out (git projects indexed from multiple worktrees).
	Worktree string
}

// Response is the outcome of a search, independent of output format.
type Response struct {
	Project  *store.Project
	Model    string
	Results  []store.SearchResult
	Fallback bool // true when embedding was unavailable and keyword search was used
}

// Search resolves the model, embeds the query and runs a vector search,
// transparently falling back to keyword search if the embedding fails.
func (s *Service) Search(ctx context.Context, req Request) (*Response, error) {
	if req.TopK <= 0 {
		req.TopK = 5
	}

	project, err := s.store.GetProject(ctx, req.Project)
	if err != nil {
		return nil, fmt.Errorf("project not found: %s", req.Project)
	}

	model := project.Model
	if req.Model != "" {
		model = req.Model
	}

	// A provider that knows the model wins; otherwise infer from the name.
	dims := embed.InferDims(model)
	if info, err := s.emb.ModelInfo(ctx, model); err == nil {
		dims = info.Dims
	}

	resp := &Response{Project: project, Model: model}

	// Keyword-only mode: skip embedding entirely and search the text bucket.
	if req.KeywordOnly {
		results, err := s.keywordSearch(ctx, project.ID, req.Query, store.KeywordDims, req.TopK, req.Worktree)
		if err != nil {
			return nil, err
		}
		resp.Results = results
		return resp, nil
	}

	vec, err := s.emb.EmbedSingle(ctx, model, req.Query)
	if err != nil {
		resp.Fallback = true
		results, kerr := s.keywordSearch(ctx, project.ID, req.Query, dims, req.TopK, req.Worktree)
		if kerr != nil {
			return nil, kerr
		}
		resp.Results = results
		return resp, nil
	}

	results, serr := s.vectorSearch(ctx, project.ID, vec, dims, req.TopK, req.Worktree)
	if serr != nil {
		return nil, serr
	}
	resp.Results = results
	return resp, nil
}

// vectorSearch runs a vector similarity search, scoped to a worktree's checked-out
// versions when worktree is set.
func (s *Service) vectorSearch(ctx context.Context, projectID int, vec []float32, dims, topK int, worktree string) ([]store.SearchResult, error) {
	if worktree != "" {
		return s.store.SearchSimilarWorktree(ctx, projectID, vec, dims, topK, worktree)
	}
	return s.store.SearchSimilar(ctx, projectID, vec, dims, topK)
}

// keywordSearch runs a keyword search, scoped to a worktree when set.
func (s *Service) keywordSearch(ctx context.Context, projectID int, query string, dims, topK int, worktree string) ([]store.SearchResult, error) {
	if worktree != "" {
		return s.store.SearchSimilarKeywordsWorktree(ctx, projectID, query, dims, topK, worktree)
	}
	return s.store.SearchSimilarKeywords(ctx, projectID, query, dims, topK)
}
