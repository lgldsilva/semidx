package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/lgldsilva/semidx/internal/rag"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
)

// searchAdapter implements rag.SearchService by delegating to search.Service.
type searchAdapter struct {
	svc     *search.Service
	project string // default project when the request doesn't specify one
}

// Search converts a rag.SearchRequest to a search.Request, calls the underlying
// search.Service, and maps the response back to rag types.
func (a *searchAdapter) Search(ctx context.Context, req rag.SearchRequest) (*rag.SearchResponse, error) {
	project := req.Project
	if project == "" {
		project = a.project
	}

	searchReq := search.Request{
		Project:       project,
		Identity:      req.Identity,
		Query:         req.Query,
		TopK:          req.TopK,
		KeywordOnly:   req.KeywordOnly,
		Worktree:      req.Worktree,
		Graph:         req.Graph,
		GraphMaxDepth: req.GraphMaxDepth,
	}

	resp, err := a.svc.Search(ctx, searchReq)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("project not found: %w — is it indexed? Run: semidx index --project <path>", err)
		}
		return nil, err
	}

	results := make([]rag.SearchResult, len(resp.Results))
	for i, r := range resp.Results {
		results[i] = rag.SearchResult{
			FilePath:  r.FilePath,
			Content:   r.Content,
			Score:     r.Score,
			StartLine: r.StartLine,
			EndLine:   r.EndLine,
		}
	}

	return &rag.SearchResponse{
		Results:  results,
		Fallback: resp.Fallback,
		Keyword:  resp.Keyword,
	}, nil
}
