// Package rag implements the Retrieval-Augmented Generation pipeline over
// semidx's semantic search index.
package rag

import "context"

// Source is a retrieved chunk used in the answer.
type Source struct {
	File      string
	StartLine int
	EndLine   int
	Content   string  // the chunk text
	Score     float64 // similarity score
	Keyword   bool    // true if score is from keyword match rather than semantic
}

// Answer is a RAG response.
type Answer struct {
	Content  string   // the LLM's answer
	Sources  []Source // chunks used to generate the answer
	Model    string   // the chat model that answered
	Fallback bool     // true if search fell back from semantic to keyword
	Keyword  bool     // true if search used keyword-only mode
}

// PipelineConfig holds RAG pipeline settings.
type PipelineConfig struct {
	TopK          int     // number of chunks to retrieve (default 5)
	MaxTokens     int     // max tokens for the LLM response (default 4096)
	Temperature   float64 // chat temperature (default 0.3)
	Model         string  // chat model to use
	Identity      string  // repo identity (git) or absolute path (docs) for search
	Worktree      string  // worktree root path for git projects
	Graph         bool    // enable graph expansion on search results (default false)
	GraphMaxDepth int     // max BFS depth for graph expansion (default 2)
	PathPrefix    string  // filter search results to files under this path prefix
}

// SearchService is the interface for semantic search (implemented by
// search.Service in cmd/chatrag/ or a test fake).
type SearchService interface {
	Search(ctx context.Context, req SearchRequest) (*SearchResponse, error)
}

// SearchRequest mirrors search.Request but decouples from the store package.
type SearchRequest struct {
	Project       string
	Query         string
	TopK          int
	Identity      string // repo identity (git) or absolute path (docs)
	Worktree      string // worktree root path for git projects
	KeywordOnly   bool   // force keyword-only search
	Graph         bool   // enable graph expansion (BFS via dependencies)
	GraphMaxDepth int    // max BFS depth for graph expansion (default 2, clamped internally)
	PathPrefix    string // filter results to files under this path prefix
}

// SearchResponse mirrors search.Response but decoupled.
type SearchResponse struct {
	Results  []SearchResult
	Fallback bool // true if fell back from semantic to keyword
	Keyword  bool // true if results came from keyword search
}

// SearchResult mirrors store.SearchResult.
type SearchResult struct {
	FilePath  string
	Content   string
	Score     float64
	StartLine int
	EndLine   int
}
