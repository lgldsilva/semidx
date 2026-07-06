package search

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/store"
)

// fakeStore implements store.Store; only the methods Search uses are overridden.
type fakeStore struct {
	store.Store
	project       *store.Project
	getErr        error
	simResults    []store.SearchResult
	kwResults     []store.SearchResult
	usedKW        bool
	usedWorktree  bool
	gotTopK       int
}

func (f *fakeStore) GetProject(ctx context.Context, name string) (*store.Project, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.project, nil
}
func (f *fakeStore) GetProjectByIdentity(_ context.Context, identity string) (*store.Project, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.project != nil && f.project.Identity == identity {
		return f.project, nil
	}
	return nil, store.ErrNotFound
}
func (f *fakeStore) ListProjects(context.Context, int, int) ([]store.Project, error) {
	if f.project != nil {
		return []store.Project{*f.project}, nil
	}
	return nil, nil
}
func (f *fakeStore) SearchSimilar(ctx context.Context, projectID int, embedding []float32, dims, topK int) ([]store.SearchResult, error) {
	f.gotTopK = topK
	return f.simResults, nil
}
func (f *fakeStore) SearchSimilarWorktree(ctx context.Context, projectID int, embedding []float32, dims, topK int, worktree string) ([]store.SearchResult, error) {
	f.usedWorktree = true
	f.gotTopK = topK
	return f.simResults, nil
}
func (f *fakeStore) SearchSimilarKeywords(ctx context.Context, projectID int, queryText string, dims, topK int) ([]store.SearchResult, error) {
	f.usedKW = true
	f.gotTopK = topK
	return f.kwResults, nil
}

// fakeEmbedder implements embed.Embedder; Search uses ModelInfo + EmbedSingle.
type fakeEmbedder struct {
	embed.Embedder
	vec      []float32
	embedErr error
	dims     int
}

func (f *fakeEmbedder) ModelInfo(ctx context.Context, model string) (*embed.ModelInfo, error) {
	if f.dims == 0 {
		return nil, errors.New("no model info")
	}
	return &embed.ModelInfo{Name: model, Dims: f.dims}, nil
}
func (f *fakeEmbedder) EmbedSingle(ctx context.Context, model, text string) ([]float32, error) {
	return f.vec, f.embedErr
}

func TestSearchVectorPath(t *testing.T) {
	st := &fakeStore{
		project:    &store.Project{ID: 1, Name: "p", Model: "bge-m3"},
		simResults: []store.SearchResult{{FilePath: "a.go", Content: "x", Score: 0.9}},
	}
	emb := &fakeEmbedder{vec: []float32{1, 2, 3}, dims: 3}
	svc := NewService(st, emb)

	resp, err := svc.Search(context.Background(), Request{Project: "p", Query: "q", TopK: 7})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if resp.Fallback {
		t.Error("Fallback should be false on the vector path")
	}
	if st.usedKW {
		t.Error("keyword search should not run when embedding succeeds")
	}
	if len(resp.Results) != 1 || resp.Results[0].FilePath != "a.go" {
		t.Errorf("results = %+v", resp.Results)
	}
	if resp.Model != "bge-m3" {
		t.Errorf("model = %q, want project default", resp.Model)
	}
	if st.gotTopK != 7 {
		t.Errorf("topK = %d, want 7", st.gotTopK)
	}
}

func TestSearchKeywordFallback(t *testing.T) {
	st := &fakeStore{
		project:   &store.Project{ID: 1, Name: "p", Model: "bge-m3"},
		kwResults: []store.SearchResult{{FilePath: "b.go", Content: "y", Score: 0.5}},
	}
	emb := &fakeEmbedder{embedErr: errors.New("offline"), dims: 3}
	svc := NewService(st, emb)

	resp, err := svc.Search(context.Background(), Request{Project: "p", Query: "q"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !resp.Fallback {
		t.Error("Fallback should be true when embedding fails")
	}
	if !st.usedKW {
		t.Error("keyword search should run on fallback")
	}
	if len(resp.Results) != 1 || resp.Results[0].FilePath != "b.go" {
		t.Errorf("results = %+v", resp.Results)
	}
	if st.gotTopK != 5 {
		t.Errorf("default topK = %d, want 5", st.gotTopK)
	}
}

func TestSearchProjectNotFound(t *testing.T) {
	st := &fakeStore{getErr: store.ErrNotFound}
	svc := NewService(st, &fakeEmbedder{vec: []float32{1}, dims: 1})
	_, err := svc.Search(context.Background(), Request{Project: "ghost", Query: "q"})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestSearchModelOverride(t *testing.T) {
	st := &fakeStore{project: &store.Project{ID: 1, Name: "p", Model: "bge-m3"}}
	emb := &fakeEmbedder{vec: []float32{1, 2, 3}, dims: 3}
	svc := NewService(st, emb)

	resp, err := svc.Search(context.Background(), Request{Project: "p", Query: "q", Model: "nomic-embed-text"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if resp.Model != "nomic-embed-text" {
		t.Errorf("model = %q, want the override", resp.Model)
	}
}

// TestSearchKeywordOnly verifies that KeywordOnly skips embedding entirely (even
// a broken embedder is never called) and does not flag the result as a fallback.
func TestSearchKeywordOnly(t *testing.T) {
	fs := &fakeStore{
		project:   &store.Project{ID: 7, Name: "p", Model: "bge-m3"},
		kwResults: []store.SearchResult{{FilePath: "a.go", Content: "x", StartLine: 1}},
	}
	// A failing embedder: if Search touched it, we'd see a fallback or an error.
	svc := NewService(fs, &fakeEmbedder{embedErr: errors.New("embedder must not be called")})

	resp, err := svc.Search(context.Background(), Request{Project: "p", Query: "q", TopK: 3, KeywordOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if !fs.usedKW {
		t.Error("KeywordOnly did not use keyword search")
	}
	if resp.Fallback {
		t.Error("KeywordOnly should not set Fallback (it's intentional, not a fallback)")
	}
	if len(resp.Results) != 1 || resp.Results[0].FilePath != "a.go" {
		t.Errorf("results = %+v", resp.Results)
	}
}

func TestSearchPropagatesRetryableError(t *testing.T) {
	st := &fakeStore{project: &store.Project{ID: 1, Name: "p", Model: "bge-m3"}}
	emb := &fakeEmbedder{embedErr: &embed.RetryableError{Err: errors.New("circuit open"), After: time.Second}, dims: 3}
	svc := NewService(st, emb)

	_, err := svc.Search(context.Background(), Request{Project: "p", Query: "q"})
	if err == nil {
		t.Fatal("expected retryable error")
	}
	var re interface{ RetryAfter() time.Duration }
	if !errors.As(err, &re) {
		t.Fatalf("expected RetryAfter error, got %T: %v", err, err)
	}
	if st.usedKW {
		t.Error("retryable embed errors must not fall back to keyword search")
	}
}

func TestSearchIgnoresWorktreeForNonGitProject(t *testing.T) {
	st := &fakeStore{
		project:    &store.Project{ID: 1, Name: "docs", Model: "bge-m3", SourceType: "docs"},
		simResults: []store.SearchResult{{FilePath: "readme.md", Content: "x", Score: 0.9}},
	}
	emb := &fakeEmbedder{vec: []float32{1, 2, 3}, dims: 3}
	svc := NewService(st, emb)

	_, err := svc.Search(context.Background(), Request{Project: "docs", Query: "q", Worktree: "/tmp/wt"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if st.usedWorktree {
		t.Error("worktree filter must be ignored for non-git projects")
	}
}
