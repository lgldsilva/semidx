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
	"log/slog"
	"math"
	"slices"
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
	cache *QueryCache
}

// NewService wires a search Service.
func NewService(s store.IndexStore, e embed.Embedder) *Service {
	return &Service{
		store: s,
		emb:   e,
		cache: NewQueryCache(5*time.Minute, 1000),
	}
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
	// Graph enables graph expansion (BFS via import edges) after the initial
	// search, adding related files discovered through the dependency graph.
	Graph bool
	// GraphMaxDepth is the maximum BFS depth for graph expansion. If <= 0, the
	// default depth of 2 is used.
	GraphMaxDepth int
	// ForceMode overrides the automatic query routing. When set, the search
	// uses the specified mode regardless of classifier output.
	ForceMode QueryType
	// HybridSearch enables parallel vector + keyword search merged via RRF.
	// When true, the vector and keyword results are fused instead of using
	// pure vector search with keyword fallback.
	HybridSearch bool
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

// Search resolves the model, classifies the query, and runs the appropriate
// search strategy: keyword-only, hybrid (RRF), or semantic vector search with
// automatic fallback. The result cache is checked before search and populated
// after search. When Graph is set, results are expanded via BFS through the
// project's dependency graph (Graph-RAG).
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

	// --- Query result cache check ---
	cKey := cacheKey(req.Query, project.ID, req.TopK, model)
	if cached, ok := s.cache.Get(cKey); ok {
		slog.Debug("search cache hit", "query", req.Query, "project", project.Name)
		resp.Results = cached
		resp.Keyword = false
		if req.Graph {
			resp.Results = s.tryGraphExpand(ctx, &req, resp.Results, project.ID, dims)
		}
		return resp, nil
	}

	if req.KeywordOnly {
		resp, err = s.searchKeywordOnly(ctx, project.ID, req, dims, worktree, resp)
	} else if req.HybridSearch {
		resp, err = s.searchHybrid(ctx, project.ID, req, model, dims, worktree, resp)
	} else if s.useKeywordFastPath(req.Query, req.ForceMode) {
		// Smart routing: identifier/path/exact queries run keyword first.
		resp, err = s.searchRouted(ctx, project.ID, req, model, dims, worktree, resp)
	} else {
		resp, err = s.searchSemantic(ctx, project.ID, req, model, dims, worktree, resp)
	}
	if err != nil {
		return nil, err
	}

	// --- Populate cache ---
	if !resp.Keyword || len(resp.Results) > 0 {
		s.cache.Set(cKey, resp.Results)
	}

	if req.Graph {
		resp.Results = s.tryGraphExpand(ctx, &req, resp.Results, project.ID, dims)
	}

	return resp, nil
}

// useKeywordFastPath determines whether a query should be routed to the
// keyword-first fast path based on its type. When ForceMode is set it
// overrides the classifier.
func (s *Service) useKeywordFastPath(query string, force QueryType) bool {
	qtype := force
	if qtype == QueryUnknown {
		qtype = ClassifyQuery(query)
	}
	return qtype == QueryIdentifier || qtype == QueryPath || qtype == QueryExact
}

// searchRouted runs keyword search first and falls back to vector search only
// when keyword returns too few results.
func (s *Service) searchRouted(ctx context.Context, projectID int, req Request, model string, dims int, worktree string, resp *Response) (*Response, error) {
	// Fast path: try keyword search first.
	kw, err := s.keywordSearch(ctx, projectID, req.Query, dims, req.TopK, worktree)
	if err == nil && len(kw) >= req.TopK {
		resp.Results = kw
		resp.Keyword = true
		return resp, nil
	}

	// Keyword returned too few results or failed — fall back to vector search.
	return s.searchSemantic(ctx, projectID, req, model, dims, worktree, resp)
}

// searchHybrid runs parallel vector + keyword search merged by RRF.
func (s *Service) searchHybrid(ctx context.Context, projectID int, req Request, model string, dims int, worktree string, resp *Response) (*Response, error) {
	results, err := s.HybridSearch(ctx, projectID, req.Query, model, dims, req.TopK, worktree)
	if err != nil {
		return nil, err
	}
	resp.Results = results
	return resp, nil
}

// tryGraphExpand runs graph expansion if Graph is set, returning the (possibly
// expanded) results list.
func (s *Service) tryGraphExpand(ctx context.Context, req *Request, results []store.SearchResult, projectID, dims int) []store.SearchResult {
	expanded, gerr := s.expandByGraph(ctx, req, results, projectID, dims)
	if gerr != nil {
		slog.Warn("graph expansion failed (continuing with original results)",
			"project", req.Project,
			"error", gerr,
		)
		return results
	}
	if len(expanded) > 0 {
		return mergeGraphResults(results, expanded)
	}
	return results
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

// expandByGraph runs BFS through the project's dependency graph from the seed
// result file paths, collecting chunks from newly discovered files with a
// decayed score. Uses an efficient recursive CTE query instead of loading all
// edges into Go memory. Returns nil (no error) when the graph is empty.
func (s *Service) expandByGraph(ctx context.Context, req *Request, seedResults []store.SearchResult, projectID, dims int) ([]store.SearchResult, error) {
	if len(seedResults) == 0 {
		return nil, nil
	}

	maxDepth := req.GraphMaxDepth
	if maxDepth <= 0 {
		maxDepth = 2
	}

	const decay = 0.85
	const floor = 0.3
	const maxGraphExpandPaths = 100 // cap chunk fetches per query (DoS guard)

	// Build seed path list.
	seedPaths := make([]string, 0, len(seedResults))
	seedPathSet := make(map[string]float64, len(seedResults))
	for _, r := range seedResults {
		seedPaths = append(seedPaths, r.FilePath)
		seedPathSet[r.FilePath] = r.Score
	}

	// Run BFS in-database via recursive CTE.
	discovered, err := s.store.FetchGraphPathsBFS(ctx, projectID, seedPaths, maxDepth)
	if err != nil {
		return nil, fmt.Errorf("fetch graph bfs: %w", err)
	}
	if len(discovered) == 0 {
		return nil, nil
	}

	// Compute decayed scores from BFS depth and seed result scores.
	expanded := make(map[string]float64, len(discovered))
	for path, depth := range discovered {
		if depth > maxDepth {
			continue
		}
		// Use the best seed score, decayed by depth.
		var bestScore float64
		for _, seedScore := range seedPathSet {
			candidate := seedScore * floatPow(decay, depth)
			if candidate > bestScore {
				bestScore = candidate
			}
		}
		if bestScore < floor {
			continue
		}
		if len(expanded) >= maxGraphExpandPaths {
			break
		}
		expanded[path] = bestScore
	}

	if len(expanded) == 0 {
		return nil, nil
	}

	// Fetch chunks for each expanded path. Graph-discovered paths from the AST
	// analyzer are directory prefixes (e.g. "internal/store/"), so we use
	// FetchChunksByDirPrefix which does a LIKE match.
	limit := 3 // representative chunks per file
	var results []store.SearchResult
	for path, score := range expanded {
		chunks, err := s.store.FetchChunksByDirPrefix(ctx, projectID, path, dims, limit)
		if err != nil {
			// Best-effort: skip files we cannot read.
			slog.Debug("expandByGraph: skip unreadable path", "path", path, "error", err)
			// Still add a placeholder so the file path appears in results.
			results = append(results, store.SearchResult{
				FilePath: path,
				Score:    score,
			})
			continue
		}
		if len(chunks) == 0 {
			// Placeholder — file known but no chunks indexed.
			results = append(results, store.SearchResult{
				FilePath: path,
				Score:    score,
			})
			continue
		}
		for _, chunk := range chunks {
			chunk.Score = score
			results = append(results, chunk)
		}
	}

	return results, nil
}

// mergeGraphResults merges original search results with graph-expanded results,
// deduplicating by FilePath. Original results keep their scores; expanded
// results keep their decayed scores. The combined list is sorted by score
// descending.
func mergeGraphResults(original, expanded []store.SearchResult) []store.SearchResult {
	dedup := make(map[string]bool, len(original))
	for _, r := range original {
		dedup[r.FilePath] = true
	}

	results := make([]store.SearchResult, len(original), len(original)+len(expanded))
	copy(results, original)

	for _, r := range expanded {
		if !dedup[r.FilePath] {
			results = append(results, r)
			dedup[r.FilePath] = true
		}
	}

	slices.SortFunc(results, func(a, b store.SearchResult) int {
		switch {
		case a.Score > b.Score:
			return -1
		case a.Score < b.Score:
			return 1
		default:
			return 0
		}
	})

	return results
}

// floatPow returns base**exp for float64 values using math.Pow.
// Named to avoid confusion with the stdlib's float64-based Pow.
func floatPow(base float64, exp int) float64 {
	return math.Pow(base, float64(exp))
}
