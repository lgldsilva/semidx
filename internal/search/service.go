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
	"github.com/lgldsilva/semidx/internal/usage"
)

// Service runs semantic searches against an IndexStore using an Embedder.
type Service struct {
	store    store.IndexStore
	emb      embed.Embedder
	reranker Reranker // optional top-K reranker (REQ-SRCH-11); nil = disabled
	usage    usage.Recorder
}

// NewService wires a search Service.
func NewService(s store.IndexStore, e embed.Embedder) *Service {
	return &Service{store: s, emb: e, usage: usage.Nop{}}
}

// WithUsage attaches a usage Recorder (product analytics). Nil is ignored.
func (s *Service) WithUsage(r usage.Recorder) *Service {
	if r != nil {
		s.usage = r
	}
	return s
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
	// VectorOnly forces the embedding/vector retriever without keyword routing,
	// keyword fusion, or fallback. It is intended for controlled evaluation.
	VectorOnly bool
	// Worktree, when set, restricts results to the file versions that worktree
	// currently has checked out (git projects indexed from multiple worktrees).
	Worktree string
	// Graph enables graph expansion (BFS via import edges) after the initial
	// search, adding related files discovered through the dependency graph.
	Graph bool
	// GraphMaxDepth is the maximum BFS depth for graph expansion. If <= 0, the
	// default depth of 2 is used.
	GraphMaxDepth int
}

// Response is the outcome of a search, independent of output format.
type Response struct {
	Project *store.Project
	Model   string
	Results []store.SearchResult
	// Route describes the retrieval path that produced the response: keyword,
	// vector, hybrid, or fallback. It is deliberately explicit so clients do
	// not infer semantic quality from the model name alone.
	Route string
	// TookMS is the end-to-end service duration, including project resolution,
	// retrieval, reranking, graph expansion, and staleness annotation.
	TookMS   int64
	Fallback bool // true when embedding was unavailable and keyword search was used
	// Keyword is true when the results came from keyword search — either an
	// explicit keyword-only request or an embedding fallback. Keyword scores are
	// lexical rank scores, not cosine similarity or probabilities, so formatters
	// label such results "keyword match" instead of a misleading percentage.
	Keyword bool
	// Degraded is true when the embedding circuit breaker was open (provider
	// temporarily unavailable) and the search served keyword results instead of
	// failing. Degraded implies Fallback and Keyword.
	Degraded bool
	// RetryAfter hints when the embedding provider may recover (only set when
	// Degraded is true).
	RetryAfter time.Duration
	// FallbackReason summarizes why the embedding failed and the search fell
	// back to keyword ("provider: class", e.g. "ollama: timeout"); only set
	// when Fallback is true — never for an explicit keyword-only request.
	FallbackReason string `json:"fallback_reason,omitempty"`
}

// Search resolves the model, embeds the query and runs a vector search,
// transparently falling back to keyword search if the embedding fails. When
// Graph is set, results are expanded via BFS through the project's dependency
// graph (Graph-RAG).
func (s *Service) Search(ctx context.Context, req Request) (*Response, error) {
	ctx, span := observ.StartSpan(ctx, "search.Service.Search")
	defer span.End()
	start := time.Now()

	if req.TopK <= 0 {
		req.TopK = 5
	}
	req.GraphMaxDepth = ClampGraphDepth(req.GraphMaxDepth)

	// Apply query-type-aware routing (REQ-AGENT-04).
	// ClassifyQuery determines whether the query looks like a path, identifier,
	// exact string, or natural language, and adjusts search parameters.
	qt := ClassifyQuery(req.Query)
	applyQueryRouting(&req, qt)
	route := "hybrid"
	if req.KeywordOnly {
		route = "keyword"
	} else if req.VectorOnly {
		route = "vector"
	} else if RoutesToKeyword(qt) {
		route = "keyword"
	}

	project, err := s.resolveProject(ctx, req)
	if err != nil {
		s.recordUsage(ctx, req, "", usage.OutcomeError, 0, "", start)
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
	// Note: the raw query text is deliberately NOT set as a span attribute
	// (privacy — see internal/usage, which only stores a query hash by default).
	span.SetAttributes(
		attribute.String("project", project.Name),
		attribute.String("model", model),
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
		// Keyword-only search must target the table where the project's chunks
		// actually live. Pass the project's recorded dims (0 lets the store
		// resolve/probe) rather than a fixed KeywordDims bucket, which only
		// exists for keyword-only-indexed projects and 500s ("relation chunks_1
		// does not exist") on a project indexed with embeddings.
		resp, err = s.searchKeywordOnly(ctx, project.ID, req, project.Dims, worktree, resp)
	} else if req.VectorOnly {
		span.SetAttributes(attribute.String("query_type", QueryNaturalLanguage.String()))
		resp, err = s.searchVectorOnly(ctx, project.ID, req, model, dims, worktree, resp)
	} else if qt := ClassifyQuery(req.Query); RoutesToKeyword(qt) {
		kwQuery := KeywordQueryForRouting(req.Query, qt)
		span.SetAttributes(attribute.String("query_type", qt.String()))
		resp, err = s.searchRoutedKeyword(ctx, project.ID, req, kwQuery, dims, worktree, resp)
	} else {
		span.SetAttributes(attribute.String("query_type", QueryNaturalLanguage.String()))
		resp, err = s.searchSemantic(ctx, project.ID, req, model, dims, worktree, resp)
	}
	if err != nil {
		s.recordUsage(ctx, req, project.Name, usage.OutcomeError, 0, "", start)
		return nil, err
	}

	s.applyRerank(ctx, req.Query, resp)
	s.applyGraphExpansion(ctx, &req, resp, project.ID, dims)
	// Prefer the caller's worktree checkout when set so staleness reflects the
	// files the agent is actually editing; otherwise use the project's path.
	root := req.Worktree
	if root == "" {
		root = project.Path
	}
	s.annotateStaleness(ctx, project, root, resp.Results)
	if resp.Fallback {
		route = "fallback"
	} else if resp.Route != "" {
		route = resp.Route
	}
	resp.Route = route
	resp.TookMS = time.Since(start).Milliseconds()

	outcome := usage.Classify(len(resp.Results), resp.Fallback, resp.Keyword)
	s.recordUsage(ctx, req, project.Name, outcome, len(resp.Results), resp.FallbackReason, start)
	return resp, nil
}

func (s *Service) searchVectorOnly(ctx context.Context, projectID int, req Request, model string, dims int, worktree string, resp *Response) (*Response, error) {
	vec, err := s.emb.EmbedSingle(ctx, model, req.Query)
	if err != nil {
		return nil, fmt.Errorf("vector-only embedding: %w", err)
	}
	results, err := s.vectorSearch(ctx, projectID, vec, dims, req.TopK, worktree)
	if err != nil {
		return nil, err
	}
	resp.Results = results
	return resp, nil
}

// recordUsage emits a usage.Event for this search attempt. Failures inside the
// recorder are swallowed (analytics must never fail a search). project may be
// "" when project resolution itself failed, in which case req.Project (the raw
// ref the caller passed) is used instead. fallbackReason is only non-empty on a
// real embedding-degradation fallback (resp.Fallback), never for keyword-only.
func (s *Service) recordUsage(ctx context.Context, req Request, project string, outcome usage.Outcome, hits int, fallbackReason string, start time.Time) {
	if s.usage == nil {
		return
	}
	if project == "" {
		project = req.Project
	}
	s.usage.Record(ctx, usage.Event{
		Project:        project,
		Source:         usage.SourceFrom(ctx),
		Outcome:        outcome,
		HitCount:       hits,
		LatencyMS:      time.Since(start).Milliseconds(),
		Keyword:        req.KeywordOnly,
		Graph:          req.Graph,
		FallbackReason: fallbackReason,
		QueryHash:      usage.HashQuery(req.Query),
		QueryText:      req.Query,
	})
}

// applyRerank reorders the top-K with the configured reranker (REQ-SRCH-11),
// before graph expansion so the strongest matches seed the BFS. No-op when no
// reranker is set or there is nothing to reorder.
func (s *Service) applyRerank(ctx context.Context, query string, resp *Response) {
	if s.reranker != nil && len(resp.Results) > 1 {
		resp.Results = s.reranker.Rerank(ctx, query, resp.Results)
	}
}

// applyGraphExpansion expands the results via the dependency graph when
// req.Graph is set, logging and continuing with the original results on failure.
func (s *Service) applyGraphExpansion(ctx context.Context, req *Request, resp *Response, projectID, dims int) {
	if !req.Graph {
		return
	}
	expanded, gerr := s.expandByGraph(ctx, req, resp.Results, projectID, dims)
	if gerr != nil {
		slog.Warn("graph expansion failed (continuing with original results)",
			"project", resp.Project.Name,
			"error", gerr,
		)
		return
	}
	if len(expanded) > 0 {
		resp.Results = mergeGraphResults(resp.Results, expanded)
	}
}

func worktreeFilter(project *store.Project, reqWorktree string) string {
	if project.SourceType == "git" {
		return reqWorktree
	}
	return ""
}

func (s *Service) searchKeywordOnly(ctx context.Context, projectID int, req Request, dims int, worktree string, resp *Response) (*Response, error) {
	results, err := s.keywordSearch(ctx, projectID, req.Query, dims, req.TopK, worktree)
	if err != nil {
		return nil, err
	}
	resp.Results = tagResultSource(results, "keyword")
	resp.Keyword = true
	return resp, nil
}

// searchRoutedKeyword runs keyword search for identifier/path/exact queries
// without calling the embedder (query routing). Results are keyword matches,
// not a fallback from a failed embed.
func (s *Service) searchRoutedKeyword(ctx context.Context, projectID int, req Request, query string, dims int, worktree string, resp *Response) (*Response, error) {
	results, err := s.keywordSearch(ctx, projectID, query, dims, req.TopK, worktree)
	if err != nil {
		return nil, err
	}
	resp.Results = tagResultSource(results, "keyword")
	resp.Keyword = true
	return resp, nil
}

func (s *Service) searchSemantic(ctx context.Context, projectID int, req Request, model string, dims int, worktree string, resp *Response) (*Response, error) {
	vec, err := s.emb.EmbedSingle(ctx, model, req.Query)
	if err != nil {
		// An open circuit (RetryableError) degrades to the same keyword fallback
		// instead of failing the search — the caller gets results now plus a
		// retry hint, rather than an error while the provider recovers.
		var re interface{ RetryAfter() time.Duration }
		if errors.As(err, &re) {
			resp.Degraded = true
			resp.RetryAfter = re.RetryAfter()
		}
		resp.Fallback = true
		resp.Keyword = true
		resp.FallbackReason = embed.SummarizeFailure(err)
		results, kerr := s.keywordSearch(ctx, projectID, req.Query, dims, req.TopK, worktree)
		if kerr != nil {
			return nil, kerr
		}
		resp.Results = tagResultSource(results, "keyword")
		return resp, nil
	}

	results, route, herr := s.hybridFuseRoute(ctx, projectID, req.Query, vec, dims, req.TopK, worktree)
	if herr != nil {
		return nil, herr
	}
	resp.Results = results
	resp.Route = route
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
// decayed score. Returns nil (no error) when the graph is empty.
//
// For scale, it prefers the SQL recursive CTE (FetchGraphPathsBFS) which
// avoids loading the full graph into Go. It computes decayed scores from
// depth and the best seed score, then falls back to the in-memory BFS when
// the CTE is unavailable or yields no results.
func (s *Service) expandByGraph(ctx context.Context, req *Request, seedResults []store.SearchResult, projectID, dims int) ([]store.SearchResult, error) {
	if len(seedResults) == 0 {
		return nil, nil
	}

	maxDepth := req.GraphMaxDepth // already clamped in Search

	const decay = 0.85
	const floor = 0.3
	const maxGraphExpandPaths = 100 // cap chunk fetches per query (DoS guard)

	seedPaths := make(map[string]bool, len(seedResults))
	seedList := make([]string, 0, len(seedResults))
	seenSeed := make(map[string]bool, len(seedResults))
	maxSeedScore := 0.0
	for _, r := range seedResults {
		if !seenSeed[r.FilePath] {
			seedList = append(seedList, r.FilePath)
			seenSeed[r.FilePath] = true
		}
		seedPaths[r.FilePath] = true
		if r.Score > maxSeedScore {
			maxSeedScore = r.Score
		}
	}
	if maxSeedScore == 0 {
		maxSeedScore = 1.0
	}

	// Prefer SQL CTE path for scale (single round trip, no full graph load).
	if paths, err := s.store.FetchGraphPathsBFS(ctx, projectID, seedList, maxDepth); err == nil && len(paths) > 0 {
		expanded := make(map[string]graphHop, len(paths))
		for path, depth := range paths {
			if len(expanded) >= maxGraphExpandPaths {
				break
			}
			score := maxSeedScore * math.Pow(decay, float64(depth))
			if score < floor {
				continue
			}
			expanded[path] = graphHop{Score: score, Depth: depth}
		}
		if len(expanded) > 0 {
			results := fetchGraphChunks(ctx, s, projectID, dims, expanded)
			if len(results) > 0 {
				return results, nil
			}
		}
		// Fall through to Go BFS if CTE gave no usable expansion.
	}

	// Fallback: load full graph and BFS in Go (covers SQLite without CTE or empty CTE result).
	graph, err := s.store.FetchGraphNeighbors(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("fetch graph neighbors: %w", err)
	}
	if len(graph) == 0 {
		return nil, nil // no graph data — nothing to expand
	}

	// Build reverse edges so BFS traverses both directions (imports and
	// imported-by).
	reverse := make(map[string][]string, len(graph))
	for src, targets := range graph {
		for _, tgt := range targets {
			reverse[tgt] = append(reverse[tgt], src)
		}
	}

	expanded := runGraphBFS(bfsParams{
		graph:     graph,
		reverse:   reverse,
		seedPaths: seedPaths,
		seeds:     seedResults,
		maxDepth:  maxDepth,
		decay:     decay,
		floor:     floor,
		maxPaths:  maxGraphExpandPaths,
	})

	if len(expanded) == 0 {
		return nil, nil
	}

	results := fetchGraphChunks(ctx, s, projectID, dims, expanded)
	return results, nil
}

// graphHop is one BFS-discovered path with its decayed score and hop depth.
type graphHop struct {
	Score float64
	Depth int
}

// bfsParams bundles the inputs to the BFS-based graph expansion.
type bfsParams struct {
	graph, reverse map[string][]string
	seedPaths      map[string]bool
	seeds          []store.SearchResult
	maxDepth       int
	decay, floor   float64
	maxPaths       int
}

// fetchGraphChunks fetches chunks for each BFS-discovered path and returns
// search results with decayed scores. Failed fetches are best-effort.
func fetchGraphChunks(ctx context.Context, s *Service, projectID, dims int, expanded map[string]graphHop) []store.SearchResult {
	const limit = 3 // representative chunks per file

	if batch, ok := s.store.(store.GraphChunkBatchStore); ok {
		if results, ok := fetchGraphChunksBatch(ctx, batch, projectID, dims, limit, expanded); ok {
			return results
		}
		// Fall through to the per-path path on error: expansion is best-effort
		// and must not fail the search.
	}

	var results []store.SearchResult
	for path, hop := range expanded {
		chunks, err := s.store.FetchChunksByDirPrefix(ctx, projectID, path, dims, limit)
		if err != nil {
			// Best-effort: skip files we cannot read.
			slog.Debug("expandByGraph: skip unreadable path", "path", path, "error", err)
			// Still add a placeholder so the file path appears in results.
			results = append(results, graphHit(path, hop, ""))
			continue
		}
		if len(chunks) == 0 {
			// Placeholder — file known but no chunks indexed.
			results = append(results, graphHit(path, hop, ""))
			continue
		}
		for _, chunk := range chunks {
			chunk.Score = hop.Score
			chunk.Source = "graph"
			chunk.GraphDepth = hop.Depth
			results = append(results, chunk)
		}
	}

	return results
}

// fetchGraphChunksBatch collects every expanded file's chunks in one round trip.
// Graph expansion can reach up to maxGraphExpandPaths files, and one query per
// file makes that a hundred sequential round trips on the hot search path.
// Reports false when the batch query fails, so the caller can fall back.
func fetchGraphChunksBatch(ctx context.Context, batch store.GraphChunkBatchStore, projectID, dims, limit int, expanded map[string]graphHop) ([]store.SearchResult, bool) {
	paths := make([]string, 0, len(expanded))
	for path := range expanded {
		paths = append(paths, path)
	}
	byPath, err := batch.FetchChunksByPaths(ctx, projectID, paths, dims, limit)
	if err != nil {
		slog.Debug("expandByGraph: batch chunk fetch failed, using per-path fallback", "error", err)
		return nil, false
	}

	results := make([]store.SearchResult, 0, len(expanded))
	for _, path := range paths {
		hop := expanded[path]
		chunks := byPath[path]
		if len(chunks) == 0 {
			// Placeholder — file known to the graph but with no indexed chunks.
			results = append(results, graphHit(path, hop, ""))
			continue
		}
		for _, chunk := range chunks {
			chunk.Score = hop.Score
			chunk.Source = "graph"
			chunk.GraphDepth = hop.Depth
			results = append(results, chunk)
		}
	}
	return results, true
}

func graphHit(path string, hop graphHop, content string) store.SearchResult {
	return store.SearchResult{
		FilePath:   path,
		Score:      hop.Score,
		Content:    content,
		Source:     "graph",
		GraphDepth: hop.Depth,
	}
}

// bfsNode represents a single node in the BFS traversal of the dependency graph.
type bfsNode struct {
	path  string
	depth int
	score float64
}

// processBFSNode examines one neighbor from a BFS node. It updates the
// expanded map when the neighbor is newly discovered or reaches a higher
// score, and returns the next BFS node to enqueue (or nil when skipped).
func processBFSNode(neighbor string, node bfsNode, p bfsParams, visited map[string]float64, expanded map[string]graphHop) *bfsNode {
	if neighbor == "" {
		return nil
	}
	newScore := node.score * p.decay
	if newScore < p.floor {
		return nil
	}
	if best, seen := visited[neighbor]; seen && best >= newScore {
		return nil
	}
	visited[neighbor] = newScore

	if !p.seedPaths[neighbor] {
		if len(expanded) >= p.maxPaths {
			return nil
		}
		nextDepth := node.depth + 1
		if curr, ok := expanded[neighbor]; !ok || newScore > curr.Score {
			expanded[neighbor] = graphHop{Score: newScore, Depth: nextDepth}
		}
	}

	return &bfsNode{path: neighbor, depth: node.depth + 1, score: newScore}
}

// runGraphBFS runs the BFS traversal through the dependency graph from seed
// paths, returning newly discovered paths with decayed scores.  maxPaths caps
// the total number of expanded paths (DoS guard).
func runGraphBFS(p bfsParams) map[string]graphHop {
	// Initialise the BFS queue with every seed result at depth 0, each with its
	// own similarity score so closer seeds influence the graph more strongly.
	queue := make([]bfsNode, 0, len(p.seeds))
	visited := make(map[string]float64, len(p.seeds)) // path -> best score seen

	for _, r := range p.seeds {
		queue = append(queue, bfsNode{path: r.FilePath, depth: 0, score: r.Score})
		visited[r.FilePath] = r.Score
	}

	expanded := make(map[string]graphHop)

	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]

		if node.depth >= p.maxDepth {
			continue
		}

		neighbors := p.graph[node.path]
		neighbors = append(neighbors, p.reverse[node.path]...)

		for _, neighbor := range neighbors {
			if next := processBFSNode(neighbor, node, p, visited, expanded); next != nil {
				queue = append(queue, *next)
			}
		}
	}

	return expanded
}

// applyQueryRouting adjusts a search Request based on query type classification.
// It is safe to call even when the classifier is uncertain — hybrid search is
// the default for QueryNaturalLanguage and QueryUnknown.
func applyQueryRouting(req *Request, qt QueryType) {
	switch qt {
	case QueryPath:
		// Path queries benefit from keyword/hybrid over pure semantic.
		// No change needed — hybrid fusion already handles this.
		// Future: boost path prefix filter.
	case QueryIdentifier:
		// Identifiers (camelCase, snake_case, dotted) may not embed well;
		// the keyword path tends to find exact matches. Still use hybrid
		// since the embedding may capture the context around the symbol.
	case QueryExact:
		// Exact quoted queries: the user wants literal matches.
		// No change — keyword path in hybrid handles quoted content.
	case QueryNaturalLanguage:
		// Default — hybrid (vector + keyword RRF). Already the default.
	}
}

func tagResultSource(results []store.SearchResult, source string) []store.SearchResult {
	for i := range results {
		if results[i].Source == "" {
			results[i].Source = source
		}
	}
	return results
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
