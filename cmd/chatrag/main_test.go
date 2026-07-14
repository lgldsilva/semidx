package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/chat"
	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/rag"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// fakeIndexStore implements a subset of store.IndexStore for testing the
// search.Service used by the adapter. Only the methods actually called by
// search.Service.Search are implemented; the rest panic with a clear message.
type fakeIndexStore struct {
	project        *store.Project
	searchResults  []store.SearchResult
	searchErr      error
	getProjectErr  error  // when set, GetProject/GetProjectByIdentity return this
	lastProjectRef string // captured project reference (name or identity)
}

var _ store.IndexStore = (*fakeIndexStore)(nil)

func (f *fakeIndexStore) Close()                                       {}
func (f *fakeIndexStore) Ping(context.Context) error                   { return nil }
func (f *fakeIndexStore) EnsureChunksTable(context.Context, int) error { return nil }
func (f *fakeIndexStore) UpsertProject(context.Context, string, string, string, int) (int, error) {
	return 0, errors.New("unimplemented")
}
func (f *fakeIndexStore) EnsureProjectIdentity(context.Context, string, string, string, string, string, int) (int, error) {
	return 0, errors.New("unimplemented")
}
func (f *fakeIndexStore) SetWorktreeFiles(context.Context, int, string, map[string]string) error {
	return errors.New("unimplemented")
}
func (f *fakeIndexStore) PruneUnreferencedFiles(context.Context, int) (int64, error) {
	return 0, errors.New("unimplemented")
}
func (f *fakeIndexStore) CreateProject(context.Context, string, string, string, string, string, int) (*store.Project, error) {
	return nil, errors.New("unimplemented")
}
func (f *fakeIndexStore) GetProject(_ context.Context, name string) (*store.Project, error) {
	f.lastProjectRef = name
	if f.getProjectErr != nil {
		return nil, f.getProjectErr
	}
	return f.project, nil
}
func (f *fakeIndexStore) GetProjectByID(_ context.Context, id int) (*store.Project, error) {
	if f.getProjectErr != nil {
		return nil, f.getProjectErr
	}
	return f.project, nil
}
func (f *fakeIndexStore) GetProjectByIdentity(_ context.Context, identity string) (*store.Project, error) {
	f.lastProjectRef = identity
	if f.getProjectErr != nil {
		return nil, f.getProjectErr
	}
	return f.project, nil
}
func (f *fakeIndexStore) ListProjects(context.Context, int, int) ([]store.Project, error) {
	if f.project != nil {
		return []store.Project{*f.project}, nil
	}
	return nil, nil
}
func (f *fakeIndexStore) DeleteProject(context.Context, string) error {
	return errors.New("unimplemented")
}
func (f *fakeIndexStore) UpdateProjectStatus(context.Context, int, string) error {
	return errors.New("unimplemented")
}
func (f *fakeIndexStore) UpsertFile(context.Context, int, string, string, int) (int, error) {
	return 0, errors.New("unimplemented")
}
func (f *fakeIndexStore) FileUpToDate(context.Context, int, string, string, int) (bool, error) {
	return false, errors.New("unimplemented")
}
func (f *fakeIndexStore) ListFileHashes(context.Context, int) (map[string]string, error) {
	return nil, errors.New("unimplemented")
}
func (f *fakeIndexStore) DeleteFileByPath(context.Context, int, string) error {
	return errors.New("unimplemented")
}
func (f *fakeIndexStore) DeleteChunksForFile(context.Context, int, int, int) error {
	return errors.New("unimplemented")
}
func (f *fakeIndexStore) InsertChunks(context.Context, int, int, []chunker.Chunk, [][]float32, int) error {
	return errors.New("unimplemented")
}
func (f *fakeIndexStore) InsertChunksTextOnly(context.Context, int, int, []chunker.Chunk, int) error {
	return errors.New("unimplemented")
}
func (f *fakeIndexStore) SearchSimilar(ctx context.Context, projectID int, embedding []float32, dims, topK int) ([]store.SearchResult, error) {
	return f.searchResults, f.searchErr
}
func (f *fakeIndexStore) SearchSimilarKeywords(ctx context.Context, projectID int, queryText string, dims, topK int) ([]store.SearchResult, error) {
	return f.searchResults, f.searchErr
}
func (f *fakeIndexStore) SearchSimilarWorktree(ctx context.Context, projectID int, embedding []float32, dims, topK int, worktree string) ([]store.SearchResult, error) {
	return f.searchResults, f.searchErr
}
func (f *fakeIndexStore) SearchSimilarKeywordsWorktree(ctx context.Context, projectID int, queryText string, dims, topK int, worktree string) ([]store.SearchResult, error) {
	return f.searchResults, f.searchErr
}
func (f *fakeIndexStore) InsertFileDependencies(context.Context, int, string, []string) error {
	return errors.New("unimplemented")
}
func (f *fakeIndexStore) FetchGraphNeighbors(context.Context, int) (map[string][]string, error) {
	return nil, errors.New("unimplemented")
}
func (f *fakeIndexStore) FetchChunksByPath(context.Context, int, string, int, int) ([]store.SearchResult, error) {
	return nil, errors.New("unimplemented")
}
func (f *fakeIndexStore) FetchChunksByDirPrefix(context.Context, int, string, int, int) ([]store.SearchResult, error) {
	return nil, errors.New("unimplemented")
}
func (f *fakeIndexStore) CountProjectFiles(_ context.Context, _ int) (int, error) {
	return 0, errors.New("unimplemented")
}
func (f *fakeIndexStore) DropAll(context.Context) error { return errors.New("unimplemented") }

func (f *fakeIndexStore) EnsureEmbeddingCacheTable(context.Context, int) error {
	return errors.New("unimplemented")
}
func (f *fakeIndexStore) LookupEmbeddingCache(context.Context, []string, string, int) (map[string][]float32, error) {
	return nil, errors.New("unimplemented")
}
func (f *fakeIndexStore) InsertEmbeddingCache(context.Context, []string, string, [][]float32, int) error {
	return errors.New("unimplemented")
}
func (f *fakeIndexStore) PruneEmbeddingCache(context.Context, int) (int64, error) {
	return 0, errors.New("unimplemented")
}

// fakeEmbedder returns a fixed vector for any embedding request.
type fakeEmbedder struct{}

func (fakeEmbedder) EmbedSingle(_ context.Context, _, _ string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}

func (f *fakeIndexStore) FetchGraphPathsBFS(ctx context.Context, projectID int, seedPaths []string, maxDepth int) (map[string]int, error) {
	return nil, nil
}

func (f *fakeIndexStore) GetProjectCommit(ctx context.Context, projectID int) (string, error) {
	return "", nil
}

func (f *fakeIndexStore) UpdateProjectCommit(ctx context.Context, projectID int, commitSHA string) error {
	return nil
}
func (fakeEmbedder) Embed(_ context.Context, _ string, _ ...string) ([][]float32, error) {
	return [][]float32{{0.1, 0.2, 0.3}}, nil
}
func (fakeEmbedder) ModelInfo(_ context.Context, _ string) (*embed.ModelInfo, error) {
	return &embed.ModelInfo{Name: "test", Dims: 3}, nil
}
func (fakeEmbedder) ListModels(_ context.Context) ([]string, error) {
	return []string{"test-model"}, nil
}

// failingEmbedder fails on every embedding operation.
type failingEmbedder struct{}

func (failingEmbedder) EmbedSingle(_ context.Context, _, _ string) ([]float32, error) {
	return nil, errors.New("embedding failed")
}
func (failingEmbedder) Embed(_ context.Context, _ string, _ ...string) ([][]float32, error) {
	return nil, errors.New("embedding failed")
}
func (failingEmbedder) ModelInfo(_ context.Context, _ string) (*embed.ModelInfo, error) {
	return nil, errors.New("embedding failed")
}
func (failingEmbedder) ListModels(_ context.Context) ([]string, error) {
	return nil, errors.New("embedding failed")
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------


func TestSearchAdapter_ConvertsTypes(t *testing.T) {
	project := &store.Project{
		ID:         1,
		Name:       "test-project",
		Model:      "test-model",
		Identity:   "test-identity",
		SourceType: "path",
	}
	dummyResults := []store.SearchResult{
		{
			FilePath:  "src/main.go",
			Content:   "package main",
			Score:     0.95,
			StartLine: 1,
			EndLine:   5,
		},
		{
			FilePath:  "src/utils.go",
			Content:   "package utils",
			Score:     0.85,
			StartLine: 1,
			EndLine:   3,
		},
	}

	fakeStore := &fakeIndexStore{
		project:       project,
		searchResults: dummyResults,
	}
	svc := search.NewService(fakeStore, fakeEmbedder{})
	adapter := &searchAdapter{svc: svc, project: "test-project"}

	resp, err := adapter.Search(context.Background(), rag.SearchRequest{
		Query:    "find the main package",
		TopK:     5,
		Identity: "test-identity",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Keyword {
		t.Error("expected Keyword=false, got true")
	}
	if resp.Fallback {
		t.Error("expected Fallback=false, got true")
	}

	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resp.Results))
	}

	// Verify the first result is correctly mapped.
	r := resp.Results[0]
	if r.FilePath != "src/main.go" {
		t.Errorf("expected FilePath 'src/main.go', got %q", r.FilePath)
	}
	if r.Content != "package main" {
		t.Errorf("expected Content 'package main', got %q", r.Content)
	}
	// The default semantic path now fuses vector + keyword results (RRF), so the
	// raw store score is re-ranked and normalized. Assert a sensible normalized
	// score instead of the raw 0.95 input; ordering and mapping are checked below.
	if r.Score <= 0 || r.Score > 1 {
		t.Errorf("expected normalized Score in (0,1], got %f", r.Score)
	}
	if r.StartLine != 1 {
		t.Errorf("expected StartLine 1, got %d", r.StartLine)
	}
	if r.EndLine != 5 {
		t.Errorf("expected EndLine 5, got %d", r.EndLine)
	}

	// Verify the second result.
	r2 := resp.Results[1]
	if r2.FilePath != "src/utils.go" {
		t.Errorf("expected FilePath 'src/utils.go', got %q", r2.FilePath)
	}
}

func TestSearchAdapter_FallbackAndKeyword(t *testing.T) {
	project := &store.Project{
		ID:    1,
		Name:  "test-project",
		Model: "test-model",
	}
	fakeStore := &fakeIndexStore{
		project: project,
		// No results — the search will return empty results from the semantic
		// path (since fakeEmbedder returns a valid vector).
		searchResults: nil,
	}
	svc := search.NewService(fakeStore, fakeEmbedder{})

	t.Run("keyword only", func(t *testing.T) {
		adapter := &searchAdapter{svc: svc}
		resp, err := adapter.Search(context.Background(), rag.SearchRequest{
			Project:     "test-project",
			Query:       "something",
			KeywordOnly: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !resp.Keyword {
			t.Error("expected Keyword=true for KeywordOnly request")
		}
	})

	t.Run("semantic search", func(t *testing.T) {
		adapter := &searchAdapter{svc: svc, project: "test-project"}
		resp, err := adapter.Search(context.Background(), rag.SearchRequest{
			Query: "find something in code",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Keyword {
			t.Error("expected Keyword=false for semantic search")
		}
		if resp.Fallback {
			t.Error("expected Fallback=false when embedding succeeds")
		}
	})
}

func TestSearchAdapter_ErrorPropagation(t *testing.T) {
	project := &store.Project{
		ID:    1,
		Name:  "test-project",
		Model: "test-model",
	}
	fakeStore := &fakeIndexStore{
		project:   project,
		searchErr: errors.New("database connection lost"),
	}
	svc := search.NewService(fakeStore, fakeEmbedder{})
	adapter := &searchAdapter{svc: svc, project: "test-project"}

	_, err := adapter.Search(context.Background(), rag.SearchRequest{
		Query: "find something in code",
	})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "database connection lost") {
		t.Errorf("expected error to contain 'database connection lost', got: %v", err)
	}
}

func TestSearchAdapter_ErrNotFoundWrapped(t *testing.T) {
	fakeStore := &fakeIndexStore{
		project:       nil,
		getProjectErr: store.ErrNotFound,
	}
	svc := search.NewService(fakeStore, fakeEmbedder{})
	adapter := &searchAdapter{svc: svc, project: "missing-project"}

	_, err := adapter.Search(context.Background(), rag.SearchRequest{
		Query: "find something in code",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "project not found") {
		t.Errorf("expected 'project not found', got: %v", msg)
	}
	if !strings.Contains(msg, "is it indexed?") {
		t.Errorf("expected indexing hint, got: %v", msg)
	}
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("errors.Is(err, store.ErrNotFound) = false, want true: %v", err)
	}
}

func TestSearchAdapter_DefaultProject(t *testing.T) {
	project := &store.Project{
		ID:    1,
		Name:  "default-project",
		Model: "test-model",
	}
	fakeStore := &fakeIndexStore{
		project: project,
	}
	svc := search.NewService(fakeStore, fakeEmbedder{})
	adapter := &searchAdapter{svc: svc, project: "default-project"}

	// Request with no project should use the adapter's default.
	resp, err := adapter.Search(context.Background(), rag.SearchRequest{
		Query: "test",
		TopK:  5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	// Verify the adapter used the default project.
	if !strings.Contains(fakeStore.lastProjectRef, "default-project") {
		t.Errorf("adapter passed project ref %q to store, want containing 'default-project'", fakeStore.lastProjectRef)
	}
}

func TestSearchAdapter_RequestProjectOverridesDefault(t *testing.T) {
	project := &store.Project{
		ID:    1,
		Name:  "override-project",
		Model: "test-model",
	}
	fakeStore := &fakeIndexStore{
		project: project,
	}
	svc := search.NewService(fakeStore, fakeEmbedder{})
	adapter := &searchAdapter{svc: svc, project: "default-project"}

	// Request with explicit project should use it instead of adapter's default.
	resp, err := adapter.Search(context.Background(), rag.SearchRequest{
		Project: "override-project",
		Query:   "test",
		TopK:    5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	// Verify the adapter used the explicit project, not the default.
	if !strings.Contains(fakeStore.lastProjectRef, "override-project") {
		t.Errorf("adapter passed project ref %q to store, want containing 'override-project'", fakeStore.lastProjectRef)
	}
}

func TestSearchAdapter_EmptyResults(t *testing.T) {
	project := &store.Project{
		ID:    1,
		Name:  "empty-project",
		Model: "test-model",
	}
	fakeStore := &fakeIndexStore{
		project:       project,
		searchResults: []store.SearchResult{},
	}
	svc := search.NewService(fakeStore, fakeEmbedder{})
	adapter := &searchAdapter{svc: svc, project: "empty-project"}

	resp, err := adapter.Search(context.Background(), rag.SearchRequest{
		Query: "nothing",
		TopK:  5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Results) != 0 {
		t.Errorf("expected 0 results, got %d", len(resp.Results))
	}
}

func TestSearchAdapter_FallbackOnEmbeddingFailure(t *testing.T) {
	project := &store.Project{
		ID:    1,
		Name:  "test-project",
		Model: "test-model",
	}
	fakeStore := &fakeIndexStore{
		project:       project,
		searchResults: []store.SearchResult{},
	}
	emb := &failingEmbedder{}
	svc := search.NewService(fakeStore, emb)
	adapter := &searchAdapter{svc: svc, project: "test-project"}

	resp, err := adapter.Search(context.Background(), rag.SearchRequest{
		Query: "find something in code",
		TopK:  5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Fallback {
		t.Error("expected Fallback=true when embedding fails")
	}
	if !resp.Keyword {
		t.Error("expected Keyword=true when embedding fails")
	}
}

// TestHandleREPLCommand_modeToggle is the regression test for the /mode fix:
// with an agent available, /mode must toggle agent -> RAG -> agent, not stay
// stuck on agent.
func TestHandleREPLCommand_modeToggle(t *testing.T) {
	hist := chat.NewHistory(10)
	convo := agent.NewConversation(10)
	mode := "agent"

	handled, cont := handleREPLCommand("/mode", hist, convo, &mode, true)
	if !handled || !cont {
		t.Fatalf("/mode should be handled and continue; handled=%v cont=%v", handled, cont)
	}
	if mode != "RAG" {
		t.Errorf("first /mode should switch agent->RAG, got %q", mode)
	}

	handleREPLCommand("/mode", hist, convo, &mode, true)
	if mode != "agent" {
		t.Errorf("second /mode should switch RAG->agent, got %q", mode)
	}
}

// TestHandleREPLCommand_modeNoAgent verifies /mode stays on RAG when no agent
// is configured.
func TestHandleREPLCommand_modeNoAgent(t *testing.T) {
	hist := chat.NewHistory(10)
	convo := agent.NewConversation(10)
	mode := "RAG"
	handleREPLCommand("/mode", hist, convo, &mode, false)
	if mode != "RAG" {
		t.Errorf("without an agent /mode must stay RAG, got %q", mode)
	}
}

func TestFormatUsage(t *testing.T) {
	if got := formatUsage(agent.Usage{}); got != "tokens: n/a" {
		t.Errorf("empty usage = %q, want n/a", got)
	}
	got := formatUsage(agent.Usage{InputTokens: 30, OutputTokens: 13, TotalTokens: 43})
	if got != "tokens: in=30 out=13 total=43" {
		t.Errorf("usage = %q", got)
	}
	withCache := formatUsage(agent.Usage{InputTokens: 30, OutputTokens: 13, TotalTokens: 43, CacheReadTokens: 12})
	if !strings.Contains(withCache, "cache(r=12 w=0)") {
		t.Errorf("cache split missing: %q", withCache)
	}
}

// TestHandleREPLCommand_clearResetsBoth verifies /clear empties both the RAG
// history and the agent conversation (multi-turn tool memory).
func TestHandleREPLCommand_clearResetsBoth(t *testing.T) {
	hist := chat.NewHistory(10)
	hist.AddUser("hi")
	convo := agent.NewConversation(10)
	convo.AddUser("hi")
	mode := "agent"

	handled, cont := handleREPLCommand("/clear", hist, convo, &mode, true)
	if !handled || !cont {
		t.Fatalf("/clear should be handled and continue; handled=%v cont=%v", handled, cont)
	}
	if len(hist.GetMessages()) != 0 {
		t.Errorf("/clear must empty RAG history, got %d messages", len(hist.GetMessages()))
	}
	if convo.Len() != 0 {
		t.Errorf("/clear must empty agent conversation, got %d messages", convo.Len())
	}
}
