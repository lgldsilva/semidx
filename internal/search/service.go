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

	vec, err := s.emb.EmbedSingle(ctx, model, req.Query)
	if err != nil {
		resp.Fallback = true
		results, kerr := s.store.SearchSimilarKeywords(ctx, project.ID, req.Query, dims, req.TopK)
		if kerr != nil {
			return nil, kerr
		}
		resp.Results = results
		return resp, nil
	}

	results, serr := s.store.SearchSimilar(ctx, project.ID, vec, dims, req.TopK)
	if serr != nil {
		return nil, serr
	}
	resp.Results = results
	return resp, nil
}
