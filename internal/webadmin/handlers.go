package webadmin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
)

const (
	msgInternalError  = "internal error"
	headerContentType = "Content-Type"
)

// errSearchFailed is the sanitized, user-safe error surfaced when a search hits
// an infrastructure failure (REQ-SRCH-08). The real cause is logged server-side;
// this is what reaches the client so DB/provider internals never leak.
var errSearchFailed = errors.New("search failed")

// --- search helpers (SPA JSON API + tests) -------------------------------------

type adminSearchHit struct {
	Project string
	store.SearchResult
	FusionScore float64
	SourceRank  int
}

type searchData struct {
	Project         string
	ResolvedProject string // canonical name after flexible resolve
	AllProjects     bool
	Query           string
	Top             int
	Results         []adminSearchHit
	Fallback        bool
	Degraded        bool          // embed circuit open — keyword results served
	RetryAfter      time.Duration // recovery hint, set when Degraded
	Ran             bool
	ProjectCount    int // set when AllProjects ran
}

// searchFlags aggregates the response-level flags across per-project searches
// so the merged all-projects response reports fallback/degraded honestly.
type searchFlags struct {
	Fallback   bool
	Degraded   bool
	RetryAfter time.Duration
}

func (f *searchFlags) absorb(resp *search.Response) {
	f.Fallback = f.Fallback || resp.Fallback
	if resp.Degraded {
		f.Degraded = true
		if resp.RetryAfter > f.RetryAfter {
			f.RetryAfter = resp.RetryAfter
		}
	}
}

func parseSearchData(r *http.Request) (searchData, int) {
	topK := 10
	if ts := strings.TrimSpace(r.URL.Query().Get("top")); ts != "" {
		if n, err := strconv.Atoi(ts); err == nil && n > 0 && n <= 100 {
			topK = n
		}
	}
	allProjects := r.URL.Query().Get("all") == "1"
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	d := searchData{
		AllProjects: allProjects,
		Query:       strings.TrimSpace(r.URL.Query().Get("q")),
		Top:         topK,
	}
	if allProjects {
		d.Project = ""
	} else {
		d.Project = project
	}
	return d, topK
}

// searchAllProjects runs the shared multi-project search service. Keeping this
// path on the same RRF implementation as REST/MCP prevents the admin UI from
// silently producing a different ranking for the same tenant/query.
func (a *Admin) searchAllProjects(ctx context.Context, d *searchData, topK int) error {
	resp, err := a.search.SearchAllProjects(ctx, search.MultiScopeRequest{
		Query: d.Query, TopK: topK, MaxPerFile: 2,
	})
	if err != nil {
		a.log.Error("all-project search failed", "err", err)
		if strings.HasPrefix(err.Error(), "list projects:") {
			return fmt.Errorf("could not list projects")
		}
		return errSearchFailed
	}
	if resp.ProjectCount == 0 {
		return fmt.Errorf("no indexed projects")
	}
	if resp.SkippedCount > 0 {
		return errSearchFailed
	}
	d.ProjectCount = resp.ProjectCount
	d.Results = make([]adminSearchHit, 0, len(resp.Results))
	for _, hit := range resp.Results {
		d.Results = append(d.Results, adminSearchHit{
			Project: hit.Project, SearchResult: hit.SearchResult,
			FusionScore: hit.FusionScore, SourceRank: hit.SourceRank,
		})
	}
	d.Fallback = resp.Fallback
	d.Degraded = resp.Degraded
	d.RetryAfter = resp.RetryAfter
	return nil
}

func (a *Admin) mergeProjectSearches(ctx context.Context, projects []store.Project, query string, topK int) ([]adminSearchHit, searchFlags, error) {
	var merged []adminSearchHit
	var flags searchFlags
	perProject := topK * 3
	if perProject < 10 {
		perProject = 10
	}
	if perProject > 100 {
		perProject = 100
	}
	for _, proj := range projects {
		hits, resp, serr := a.searchProjectHits(ctx, proj, query, perProject)
		if serr != nil {
			return nil, searchFlags{}, serr
		}
		if resp != nil {
			flags.absorb(resp)
		}
		merged = append(merged, hits...)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Score > merged[j].Score })
	if len(merged) > topK {
		merged = merged[:topK]
	}
	return merged, flags, nil
}

func (a *Admin) searchProjectHits(ctx context.Context, proj store.Project, query string, topK int) ([]adminSearchHit, *search.Response, error) {
	req := search.Request{Query: query, TopK: topK}
	if proj.Identity != "" {
		req.Identity = proj.Identity
	} else {
		req.Project = proj.Name
	}
	resp, err := a.search.Search(ctx, req)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil, nil
		}
		// Never propagate the raw error to the all-projects response body
		// (REQ-SRCH-08): log the detail, return the sanitized sentinel.
		a.log.Error("search failed", "project", proj.Name, "err", err)
		return nil, nil, errSearchFailed
	}
	hits := make([]adminSearchHit, 0, len(resp.Results))
	for _, hit := range resp.Results {
		hits = append(hits, adminSearchHit{Project: resp.Project.Name, SearchResult: hit})
	}
	return hits, resp, nil
}
