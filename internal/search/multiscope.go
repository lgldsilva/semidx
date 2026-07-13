package search

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/lgldsilva/semidx/internal/projectref"
	"github.com/lgldsilva/semidx/internal/store"
)

// MultiScopeRequest searches across multiple project identities with optional
// path filtering.
type MultiScopeRequest struct {
	Identities    []string // project identities (git or path:identity); empty = all
	Query         string
	TopK          int
	Graph         bool
	GraphMaxDepth int
	KeywordOnly   bool
	MaxPerFile    int // cap chunks per file for diversity (0 = no limit)
	MaxPerProject int // cap results per project for diversity (0 = no limit)
}

// MultiResult is one fused search hit tagged with the project identity it came
// from. Provenance lives here (in the envelope), NOT in store.SearchResult,
// which stays clean and free of the internal scope-label prefix.
type MultiResult struct {
	store.SearchResult
	Project string `json:"project"` // project identity the hit came from
}

// MultiResponse is a fused result from multi-scope search, with provenance.
type MultiResponse struct {
	Results  []MultiResult
	Fallback bool
	Keyword  bool
}

// SearchMulti searches across multiple project identities and fuses results
// using cross-project RRF. Each result carries its project identity in
// the FilePath scope label — provenance is in the envelope, NOT in
// store.SearchResult (which stays clean).
func (s *Service) SearchMulti(ctx context.Context, req MultiScopeRequest) (*MultiResponse, error) {
	if len(req.Identities) == 0 {
		return nil, fmt.Errorf("no project identities specified")
	}
	if req.TopK <= 0 {
		req.TopK = 5
	}

	var allResults []store.SearchResult
	// Aggregate the per-search flags: if any sub-search fell back to keyword or
	// reported a keyword ranking, the fused response must say so — a client
	// mustn't receive keyword/fallback results silently labeled as semantic.
	var anyFallback, anyKeyword bool

	for _, ident := range req.Identities {
		// Search one project at a time.
		one := Request{
			Identity:      ident,
			Query:         req.Query,
			TopK:          req.TopK * 2, // over-fetch per project for fusion quality
			Graph:         req.Graph,
			GraphMaxDepth: req.GraphMaxDepth,
			KeywordOnly:   req.KeywordOnly,
		}
		resp, err := s.Search(ctx, one)
		if err != nil {
			// Best-effort: skip projects that error; log for observability.
			slog.Warn("search failed for project", "identity", ident, "error", err)
			continue
		}
		if resp.Fallback {
			anyFallback = true
		}
		if resp.Keyword {
			anyKeyword = true
		}

		// Tag results with project label using null-byte separator so
		// identities containing ':' (e.g. "path:/abs/path") or '/' are safe.
		for i := range resp.Results {
			resp.Results[i].FilePath = fmt.Sprintf("%s\x00%s", ident, resp.Results[i].FilePath)
		}
		allResults = append(allResults, resp.Results...)
	}

	// Fuse by score descending with diversity caps. True cross-project RRF
	// requires rank maps per identity and is deferred to a follow-up.
	// Note: each sub-search does NOT call applyQueryRouting — if routing
	// gains real weight, SearchMulti must route per sub-search too.
	return fuseResults(allResults, req.MaxPerFile, req.MaxPerProject, req.TopK, anyFallback, anyKeyword), nil
}

// SearchAllProjects searches every indexed project (deduped by identity) and
// fuses the results the same way SearchMulti does, tagging each hit with its
// project. It powers the global (cross-project) chat, where the agent is not
// bound to a single project. Git projects search by identity; document/push
// projects (no identity) search by name.
func (s *Service) SearchAllProjects(ctx context.Context, req MultiScopeRequest) (*MultiResponse, error) {
	projects, err := s.store.ListProjects(ctx, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	projects = projectref.UniqueByIdentity(projects)
	if len(projects) == 0 {
		return &MultiResponse{}, nil
	}
	if req.TopK <= 0 {
		req.TopK = 5
	}

	var allResults []store.SearchResult
	var anyFallback, anyKeyword bool
	for _, p := range projects {
		one := Request{
			Query:         req.Query,
			TopK:          req.TopK * 2, // over-fetch per project for fusion quality
			Graph:         req.Graph,
			GraphMaxDepth: req.GraphMaxDepth,
			KeywordOnly:   req.KeywordOnly,
		}
		label := p.Identity
		if p.Identity != "" {
			one.Identity = p.Identity
		} else {
			one.Project = p.Name
			label = p.Name
		}
		resp, err := s.Search(ctx, one)
		if err != nil {
			slog.Warn("search failed for project", "project", p.Name, "error", err)
			continue
		}
		if resp.Fallback {
			anyFallback = true
		}
		if resp.Keyword {
			anyKeyword = true
		}
		for i := range resp.Results {
			resp.Results[i].FilePath = fmt.Sprintf("%s\x00%s", label, resp.Results[i].FilePath)
		}
		allResults = append(allResults, resp.Results...)
	}
	return fuseResults(allResults, req.MaxPerFile, req.MaxPerProject, req.TopK, anyFallback, anyKeyword), nil
}

// fuseResults sorts the tagged results by score, applies diversity caps, and
// splits the internal "identity\x00path" provenance prefix back into an explicit
// Project field and a clean FilePath (the \x00 label must never leak). Shared by
// SearchMulti and SearchAllProjects.
func fuseResults(allResults []store.SearchResult, maxPerFile, maxPerProject, topK int, anyFallback, anyKeyword bool) *MultiResponse {
	if len(allResults) == 0 {
		return &MultiResponse{Fallback: anyFallback, Keyword: anyKeyword}
	}
	slices.SortFunc(allResults, func(a, b store.SearchResult) int {
		if a.Score > b.Score {
			return -1
		}
		if a.Score < b.Score {
			return 1
		}
		return 0
	})
	fused := applyDiversity(allResults, maxPerFile, maxPerProject, topK)
	if fused == nil {
		fused = allResults
		if len(fused) > topK {
			fused = fused[:topK]
		}
	}
	results := make([]MultiResult, len(fused))
	for i, r := range fused {
		proj, file := splitProvenance(r.FilePath)
		r.FilePath = file
		results[i] = MultiResult{SearchResult: r, Project: proj}
	}
	return &MultiResponse{Results: results, Fallback: anyFallback, Keyword: anyKeyword}
}

// applyDiversity caps results per file and per project (identified by prefix
// before the first ':').
func applyDiversity(results []store.SearchResult, maxPerFile, maxPerProject, topK int) []store.SearchResult {
	if maxPerFile <= 0 && maxPerProject <= 0 {
		return nil // no caps
	}
	if maxPerFile <= 0 {
		maxPerFile = topK + 1
	}
	if maxPerProject <= 0 {
		maxPerProject = topK + 1
	}

	fileCount := make(map[string]int)
	projectCount := make(map[string]int)
	var out []store.SearchResult
	for _, r := range results {
		proj, file := splitProvenance(r.FilePath)
		if projectCount[proj] >= maxPerProject {
			continue
		}
		if fileCount[file] >= maxPerFile {
			continue
		}
		projectCount[proj]++
		fileCount[file]++
		out = append(out, r)
		if len(out) >= topK {
			break
		}
	}
	return out
}

// splitProvenance splits a "\x00"-delimited "project\x00path" back to (project, file).
func splitProvenance(fp string) (string, string) {
	for i := 0; i < len(fp); i++ {
		if fp[i] == 0 {
			return fp[:i], fp[i+1:]
		}
	}
	return "", fp
}
