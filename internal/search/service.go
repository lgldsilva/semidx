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
	// Graph enables graph expansion (BFS via import edges) after the initial
	// search, adding related files discovered through the dependency graph.
	Graph bool
	// GraphMaxDepth is the maximum BFS depth for graph expansion. If <= 0, the
	// default depth of 2 is used.
	GraphMaxDepth int
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
// transparently falling back to keyword search if the embedding fails. When
// Graph is set, results are expanded via BFS through the project's dependency
// graph (Graph-RAG).
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
		resp, err = s.searchKeywordOnly(ctx, project.ID, req, dims, worktree, resp)
	} else {
		resp, err = s.searchSemantic(ctx, project.ID, req, model, dims, worktree, resp)
	}
	if err != nil {
		return nil, err
	}

	if req.Graph {
		expanded, gerr := s.expandByGraph(ctx, &req, resp.Results, project.ID, dims)
		if gerr != nil {
			slog.Warn("graph expansion failed (continuing with original results)",
				"project", project.Name,
				"error", gerr,
			)
		} else if len(expanded) > 0 {
			resp.Results = mergeGraphResults(resp.Results, expanded)
		}
	}

	return resp, nil
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
// decayed score. Returns nil (no error) when the graph is empty.
func (s *Service) expandByGraph(ctx context.Context, req *Request, seedResults []store.SearchResult, projectID, dims int) ([]store.SearchResult, error) {
	if len(seedResults) == 0 {
		return nil, nil
	}

	// Fetch the full dependency graph for the project.
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

	maxDepth := req.GraphMaxDepth
	if maxDepth <= 0 {
		maxDepth = 2
	}

	const decay = 0.85
	const floor = 0.3
	const maxGraphExpandPaths = 100 // cap chunk fetches per query (DoS guard)

	seedPaths := make(map[string]bool, len(seedResults))
	for _, r := range seedResults {
		seedPaths[r.FilePath] = true
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
func fetchGraphChunks(ctx context.Context, s *Service, projectID, dims int, expanded map[string]float64) []store.SearchResult {
	const limit = 3 // representative chunks per file
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

	return results
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
func processBFSNode(neighbor string, node bfsNode, p bfsParams, visited map[string]float64, expanded map[string]float64) *bfsNode {
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
		if curr, ok := expanded[neighbor]; !ok || newScore > curr {
			expanded[neighbor] = newScore
		}
	}

	return &bfsNode{path: neighbor, depth: node.depth + 1, score: newScore}
}

// runGraphBFS runs the BFS traversal through the dependency graph from seed
// paths, returning newly discovered paths with decayed scores.  maxPaths caps
// the total number of expanded paths (DoS guard).
func runGraphBFS(p bfsParams) map[string]float64 {
	// Initialise the BFS queue with every seed result at depth 0, each with its
	// own similarity score so closer seeds influence the graph more strongly.
	queue := make([]bfsNode, 0, len(p.seeds))
	visited := make(map[string]float64, len(p.seeds)) // path -> best score seen

	for _, r := range p.seeds {
		queue = append(queue, bfsNode{path: r.FilePath, depth: 0, score: r.Score})
		visited[r.FilePath] = r.Score
	}

	expanded := make(map[string]float64)

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
