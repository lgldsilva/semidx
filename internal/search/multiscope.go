package search

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/lgldsilva/semidx/internal/projectref"
	"github.com/lgldsilva/semidx/internal/store"
)

// MultiScopeRequest searches across multiple project identities with optional
// path filtering.
type MultiScopeRequest struct {
	Identities    []string // project identities (git or path:identity); empty = all
	Projects      []string // project names for document/push projects or friendly API callers
	All           bool     // search every project in the active workspace
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
	Project     string  `json:"project"`      // project identity the hit came from
	SourceRank  int     `json:"source_rank"`  // rank inside that project's result list
	FusionScore float64 `json:"fusion_score"` // reciprocal-rank fusion score
}

const reciprocalRankConstant = 60

type rankedResult struct {
	result     store.SearchResult
	project    string
	sourceRank int
	fusion     float64
}

type multiProjectScope struct {
	request Request
	label   string
}

// MultiResponse is a fused result from multi-scope search, with provenance.
type MultiResponse struct {
	Results      []MultiResult
	ProjectCount int
	SkippedCount int // projects that could not be searched; results remain best-effort
	Fallback     bool
	Keyword      bool
	// Degraded is true when at least one sub-search degraded to keyword results
	// because the embedding circuit was open. RetryAfter is the largest recovery
	// hint across degraded sub-searches.
	Degraded   bool
	RetryAfter time.Duration
}

// aggFlags accumulates the response-level flags across sub-searches so the
// fused response reports fallback/keyword/degraded honestly — a client mustn't
// receive keyword or degraded results silently labeled as semantic.
type aggFlags struct {
	fallback, keyword, degraded bool
	retryAfter                  time.Duration
}

func (f *aggFlags) absorb(resp *Response) {
	f.fallback = f.fallback || resp.Fallback
	f.keyword = f.keyword || resp.Keyword
	if resp.Degraded {
		f.degraded = true
		if resp.RetryAfter > f.retryAfter {
			f.retryAfter = resp.RetryAfter
		}
	}
}

// SearchMulti searches across multiple project identities and fuses results
// using cross-project RRF. Each result carries its project identity in
// the FilePath scope label — provenance is in the envelope, NOT in
// store.SearchResult (which stays clean).
func (s *Service) SearchMulti(ctx context.Context, req MultiScopeRequest) (*MultiResponse, error) {
	if req.All {
		return s.SearchAllProjects(ctx, req)
	}
	if len(req.Identities) == 0 && len(req.Projects) == 0 {
		return nil, fmt.Errorf("no project identities specified")
	}
	if req.TopK <= 0 {
		req.TopK = 5
	}

	scopes := make([]multiProjectScope, 0, len(req.Identities)+len(req.Projects))
	for _, ident := range req.Identities {
		scopes = append(scopes, multiProjectScope{
			request: Request{Identity: ident}, label: ident,
		})
	}
	for _, project := range req.Projects {
		scopes = append(scopes, multiProjectScope{
			request: Request{Project: project}, label: project,
		})
	}
	projectCount := len(scopes)

	var allResults []rankedResult
	var flags aggFlags
	skipped := 0

	for _, scope := range scopes {
		// Search one project at a time.
		one := scope.request
		one.Query = req.Query
		one.TopK = req.TopK * 2 // over-fetch per project for fusion quality
		one.Graph = req.Graph
		one.GraphMaxDepth = req.GraphMaxDepth
		one.KeywordOnly = req.KeywordOnly
		resp, err := s.Search(ctx, one)
		if err != nil {
			// Best-effort: skip projects that error; log for observability.
			slog.Warn("search failed for project", "project", scope.label, "error", err)
			skipped++
			continue
		}
		flags.absorb(resp)

		// Tag results with project label using null-byte separator so
		// identities containing ':' (e.g. "path:/abs/path") or '/' are safe.
		for i, result := range resp.Results {
			result.FilePath = fmt.Sprintf("%s\x00%s", scope.label, result.FilePath)
			allResults = append(allResults, rankedResult{
				result: result, project: scope.label, sourceRank: i + 1,
				fusion: 1 / float64(reciprocalRankConstant+i+1),
			})
		}
	}

	// Note: each sub-search does NOT call applyQueryRouting — if routing
	// gains real weight, SearchMulti must route per sub-search too.
	resp := fuseRankedResults(allResults, req.MaxPerFile, req.MaxPerProject, req.TopK, flags)
	resp.ProjectCount = projectCount
	resp.SkippedCount = skipped
	return resp, nil

}

// SearchAllProjects searches every indexed project in the active workspace
// (deduped by identity) and
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

	var allResults []rankedResult
	var flags aggFlags
	skipped := 0
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
			skipped++
			continue
		}
		flags.absorb(resp)
		for i, result := range resp.Results {
			result.FilePath = fmt.Sprintf("%s\x00%s", label, result.FilePath)
			allResults = append(allResults, rankedResult{
				result: result, project: label, sourceRank: i + 1,
				fusion: 1 / float64(reciprocalRankConstant+i+1),
			})
		}
	}
	resp := fuseRankedResults(allResults, req.MaxPerFile, req.MaxPerProject, req.TopK, flags)
	resp.ProjectCount = len(projects)
	resp.SkippedCount = skipped
	return resp, nil
}

// fuseResults keeps the old internal helper useful to callers/tests that pass
// tagged results directly. Production multi-project searches use rankedResult
// so each project's rank contributes independently through RRF.
func fuseResults(allResults []store.SearchResult, maxPerFile, maxPerProject, topK int, flags aggFlags) *MultiResponse {
	byProjectRank := make(map[string]int)
	ranked := make([]rankedResult, 0, len(allResults))
	for _, result := range allResults {
		project, _ := splitProvenance(result.FilePath)
		byProjectRank[project]++
		ranked = append(ranked, rankedResult{
			result: result, project: project,
			sourceRank: byProjectRank[project],
			fusion:     1 / float64(reciprocalRankConstant+byProjectRank[project]),
		})
	}
	return fuseRankedResults(ranked, maxPerFile, maxPerProject, topK, flags)
}

// fuseRankedResults applies reciprocal-rank fusion across project result lists.
// Similarity scores are deliberately retained in SearchResult.Score for
// explainability; FusionScore is the cross-project ordering score.
func fuseRankedResults(allResults []rankedResult, maxPerFile, maxPerProject, topK int, flags aggFlags) *MultiResponse {
	out := &MultiResponse{
		Fallback: flags.fallback, Keyword: flags.keyword,
		Degraded: flags.degraded, RetryAfter: flags.retryAfter,
	}
	if len(allResults) == 0 {
		return out
	}
	if topK <= 0 {
		topK = 5
	}

	// A caller may request the same identity more than once. Treat the same
	// chunk in that project as one candidate while adding its independent rank
	// contributions, just as RRF does for multiple retrievers.
	byCandidate := make(map[string]rankedResult, len(allResults))
	for _, candidate := range allResults {
		candidate.fusion = 1 / float64(reciprocalRankConstant+candidate.sourceRank)
		key := candidateKey(candidate)
		if previous, ok := byCandidate[key]; ok {
			previous.fusion += candidate.fusion
			if candidate.result.Score > previous.result.Score {
				previous.result = candidate.result
			}
			byCandidate[key] = previous
			continue
		}
		byCandidate[key] = candidate
	}

	ranked := make([]rankedResult, 0, len(byCandidate))
	for _, candidate := range byCandidate {
		ranked = append(ranked, candidate)
	}
	slices.SortFunc(ranked, func(a, b rankedResult) int {
		if a.fusion > b.fusion {
			return -1
		}
		if a.fusion < b.fusion {
			return 1
		}
		if a.result.Score > b.result.Score {
			return -1
		}
		if a.result.Score < b.result.Score {
			return 1
		}
		return compareCandidates(a, b)
	})

	selected := ranked
	if maxPerFile > 0 || maxPerProject > 0 {
		selected = applyRankedDiversity(ranked, maxPerFile, maxPerProject, topK)
	} else if len(selected) > topK {
		selected = selected[:topK]
	}

	out.Results = make([]MultiResult, 0, len(selected))
	for _, candidate := range selected {
		project, file := splitProvenance(candidate.result.FilePath)
		candidate.result.FilePath = file
		out.Results = append(out.Results, MultiResult{
			SearchResult: candidate.result,
			Project:      project, SourceRank: candidate.sourceRank, FusionScore: candidate.fusion,
		})
	}
	return out
}

func candidateKey(candidate rankedResult) string {
	return fmt.Sprintf("%s\x00%s\x00%d\x00%d", candidate.project,
		candidate.result.FilePath, candidate.result.StartLine, candidate.result.EndLine)
}

func compareCandidates(a, b rankedResult) int {
	if a.project < b.project {
		return -1
	}
	if a.project > b.project {
		return 1
	}
	if a.result.FilePath < b.result.FilePath {
		return -1
	}
	if a.result.FilePath > b.result.FilePath {
		return 1
	}
	if a.result.StartLine < b.result.StartLine {
		return -1
	}
	if a.result.StartLine > b.result.StartLine {
		return 1
	}
	return 0
}

func applyRankedDiversity(results []rankedResult, maxPerFile, maxPerProject, topK int) []rankedResult {
	if maxPerFile <= 0 {
		maxPerFile = topK + 1
	}
	if maxPerProject <= 0 {
		maxPerProject = topK + 1
	}
	fileCount := make(map[string]int)
	projectCount := make(map[string]int)
	out := make([]rankedResult, 0, min(topK, len(results)))
	for _, result := range results {
		project, file := splitProvenance(result.result.FilePath)
		if projectCount[project] >= maxPerProject || fileCount[project+"\x00"+file] >= maxPerFile {
			continue
		}
		projectCount[project]++
		fileCount[project+"\x00"+file]++
		out = append(out, result)
		if len(out) >= topK {
			break
		}
	}
	return out
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
