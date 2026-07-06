// Package search unifies the semantic-search flow shared by the CLI (search and
// sgrep) and the MCP server, which previously each reimplemented it. A single
// Service resolves the model, embeds the query, runs the vector search, and
// falls back to keyword search when embeddings are unavailable; Formatters then
// render the same Response in different output styles.
package search

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/observ"
	"github.com/lgldsilva/semidx/internal/projectref"
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
	// Identity, when set, resolves the project by its unique identity (git
	// identity or "path:<abs>") instead of the collision-prone Project name.
	Identity string
	Query    string
	Model    string // optional; overrides the project's stored model
	TopK     int    // <= 0 defaults to 5
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
	// Keyword is true when the results came from keyword search — either an
	// explicit keyword-only request or an embedding fallback. Keyword scores are a
	// constant placeholder, not a similarity, so formatters label such results
	// "keyword match" instead of a misleading percentage.
	Keyword bool
}

// Search resolves the model, embeds the query and runs a vector search,
// transparently falling back to keyword search if the embedding fails.
func (s *Service) Search(ctx context.Context, req Request) (*Response, error) {
	ctx, span := observ.StartSpan(ctx, "search.Service.Search")
	defer span.End()

	if req.TopK <= 0 {
		req.TopK = 5
	}

	project, err := s.resolveProject(ctx, req)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		ref := req.Project
		if req.Identity != "" {
			ref = req.Identity
		}
		return nil, fmt.Errorf("project lookup failed for %s: %w", ref, err)
	}

	model := project.Model
	if req.Model != "" {
		model = req.Model
	}
	span.SetAttributes(
		attribute.String("project", project.Name),
		attribute.String("model", model),
		attribute.String("query", req.Query),
		attribute.Int("topk", req.TopK),
	)

	// A provider that knows the model wins; otherwise infer from the name.
	dims := embed.InferDims(model)
	if info, err := s.emb.ModelInfo(ctx, model); err == nil {
		dims = info.Dims
	}

	resp := &Response{Project: project, Model: model}
	worktree := worktreeFilter(project, req.Worktree)

	if req.KeywordOnly {
		return s.searchKeywordOnly(ctx, project.ID, req, dims, worktree, resp)
	}
	return s.searchSemantic(ctx, project.ID, req, model, dims, worktree, resp)
}

func worktreeFilter(project *store.Project, reqWorktree string) string {
	if project.SourceType == "git" {
		return reqWorktree
	}
	return ""
}

func (s *Service) searchKeywordOnly(ctx context.Context, projectID int, req Request, dims int, worktree string, resp *Response) (*Response, error) {
	results, err := s.keywordSearch(ctx, projectID, req.Query, store.KeywordDims, req.TopK, worktree)
	if err != nil {
		return nil, err
	}
	resp.Results = results
	resp.Keyword = true
	return resp, nil
}

func (s *Service) searchSemantic(ctx context.Context, projectID int, req Request, model string, dims int, worktree string, resp *Response) (*Response, error) {
	vec, err := s.emb.EmbedSingle(ctx, model, req.Query)
	if err != nil {
		// Propagate retryable errors (e.g. circuit breaker open) directly
		// instead of falling back to keyword search — the caller should
		// back off and retry after the indicated duration.
		var re interface{ RetryAfter() time.Duration }
		if errors.As(err, &re) {
			return nil, err
		}
		resp.Fallback = true
		resp.Keyword = true
		results, kerr := s.keywordSearch(ctx, projectID, req.Query, dims, req.TopK, worktree)
		if kerr != nil {
			return nil, kerr
		}
		resp.Results = results
		return resp, nil
	}

	results, serr := s.vectorSearch(ctx, projectID, vec, dims, req.TopK, worktree)
	if serr != nil {
		return nil, serr
	}
	resp.Results = results
	return resp, nil
}

// resolveProject looks the project up by unique identity when the request carries
// one, else by flexible ref (path, identity, or name).
func (s *Service) resolveProject(ctx context.Context, req Request) (*store.Project, error) {
	if req.Identity != "" {
		return s.store.GetProjectByIdentity(ctx, req.Identity)
	}
	if req.Project != "" {
		return projectref.Resolve(ctx, s.store, req.Project)
	}
	return nil, store.ErrNotFound
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
