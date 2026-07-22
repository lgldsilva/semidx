package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// fakeEmbedder returns a fixed embedding so search.Service can run.
type fakeEmbedder struct{}

func (fakeEmbedder) ModelInfo(_ context.Context, model string) (*embed.ModelInfo, error) {
	return &embed.ModelInfo{Name: model, Dims: 8}, nil
}

func (fakeEmbedder) Embed(_ context.Context, _ string, inputs ...string) ([][]float32, error) {
	r := make([][]float32, len(inputs))
	for i := range inputs {
		r[i] = []float32{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8}
	}
	return r, nil
}

func (fakeEmbedder) EmbedSingle(_ context.Context, _, _ string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8}, nil
}

func (fakeEmbedder) ListModels(_ context.Context) ([]string, error) {
	return []string{"test-model"}, nil
}

// fakeSearchStore implements the store.IndexStore subset needed by the search
// service and the index/list tools.  Embedding the full interface means any
// unimplemented method panics (safe for tests that never call them).
type fakeSearchStore struct {
	store.IndexStore
	projects   map[string]*store.Project // keyed by name
	identities map[string]*store.Project // keyed by identity
	fileHashes map[int]map[string]string // projectID -> hash -> path
}

func newFakeSearchStore() *fakeSearchStore {
	return &fakeSearchStore{
		projects:   make(map[string]*store.Project),
		identities: make(map[string]*store.Project),
		fileHashes: make(map[int]map[string]string),
	}
}

func (f *fakeSearchStore) addProject(p *store.Project) {
	f.projects[p.Name] = p
	f.identities[p.Identity] = p
}

func (f *fakeSearchStore) GetProject(_ context.Context, name string) (*store.Project, error) {
	p, ok := f.projects[name]
	if !ok {
		return nil, store.ErrNotFound
	}
	return p, nil
}

func (f *fakeSearchStore) GetProjectByIdentity(_ context.Context, identity string) (*store.Project, error) {
	p, ok := f.identities[identity]
	if !ok {
		return nil, store.ErrNotFound
	}
	return p, nil
}

func (f *fakeSearchStore) ListProjects(_ context.Context, _, _ int) ([]store.Project, error) {
	out := make([]store.Project, 0, len(f.projects))
	for _, p := range f.projects {
		out = append(out, *p)
	}
	return out, nil
}

func (f *fakeSearchStore) SearchSimilar(_ context.Context, _ int, _ []float32, _, _ int) ([]store.SearchResult, error) {
	return []store.SearchResult{
		{FilePath: "main.go", Content: "package main", Score: 0.95, StartLine: 1, EndLine: 3},
	}, nil
}

func (f *fakeSearchStore) SearchSimilarKeywords(_ context.Context, _ int, _ string, _, _ int) ([]store.SearchResult, error) {
	return []store.SearchResult{
		{FilePath: "main.go", Content: "package main", Score: 0.80, StartLine: 1, EndLine: 3},
	}, nil
}

func (f *fakeSearchStore) ListFileHashes(_ context.Context, projectID int) (map[string]string, error) {
	if h, ok := f.fileHashes[projectID]; ok {
		return h, nil
	}
	return map[string]string{}, nil
}

func (f *fakeSearchStore) ListFileHashesWithTime(ctx context.Context, projectID int) (map[string]store.FileHashInfo, error) {
	hashes, err := f.ListFileHashes(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]store.FileHashInfo, len(hashes))
	for path, hash := range hashes {
		out[path] = store.FileHashInfo{Hash: hash}
	}
	return out, nil
}

// fakeNilStore returns store.ErrNotFound for every project lookup.
// Methods are used via store.IndexStore interface dispatch by tool.Run calls.
type fakeNilStore struct {
	store.IndexStore
}

func (fakeNilStore) GetProject(_ context.Context, _ string) (*store.Project, error) {
	return nil, store.ErrNotFound
}
func (fakeNilStore) GetProjectByIdentity(_ context.Context, _ string) (*store.Project, error) {
	return nil, store.ErrNotFound
}
func (fakeNilStore) ListProjects(_ context.Context, _, _ int) ([]store.Project, error) {
	return nil, nil
}

// fakeEmptyStore returns empty results for project lookups.
// Methods are used via store.IndexStore interface dispatch by tool.Run calls.
type fakeEmptyStore struct {
	store.IndexStore
}

func (s fakeEmptyStore) GetProject(_ context.Context, name string) (*store.Project, error) {
	if name == "" {
		return nil, store.ErrNotFound
	}
	return &store.Project{Name: name, Path: "/tmp/test", Identity: "test-id", SourceType: "git"}, nil
}
func (s fakeEmptyStore) GetProjectByIdentity(_ context.Context, _ string) (*store.Project, error) {
	return nil, store.ErrNotFound
}
func (s fakeEmptyStore) ListProjects(_ context.Context, _, _ int) ([]store.Project, error) {
	return nil, nil
}
func (s fakeEmptyStore) ListFileHashes(_ context.Context, _ int) (map[string]string, error) {
	return map[string]string{}, nil
}

// TestFakeStoresExerciseInterfaceMethods directly calls each fake's methods to
// satisfy the unused-function linter — these methods ARE used at runtime via
// store.IndexStore interface dispatch but the linter cannot trace that.
func TestFakeStoresExerciseInterfaceMethods(t *testing.T) {
	t.Run("fakeNilStore", func(t *testing.T) {
		var s store.IndexStore = fakeNilStore{}
		_, err1 := s.GetProject(context.Background(), "x")
		_, err2 := s.GetProjectByIdentity(context.Background(), "x")
		_, err3 := s.ListProjects(context.Background(), 0, 0)
		if err1 == nil || err2 == nil || err3 != nil {
			t.Error("unexpected fake result")
		}
	})
	t.Run("fakeEmptyStore", func(t *testing.T) {
		var s store.IndexStore = fakeEmptyStore{}
		p, err := s.GetProject(context.Background(), "p")
		if err != nil || p == nil {
			t.Error("fakeEmptyStore.GetProject should succeed")
		}
		_, err = s.GetProjectByIdentity(context.Background(), "p")
		if err == nil {
			t.Error("fakeEmptyStore.GetProjectByIdentity should fail")
		}
		_, err = s.ListProjects(context.Background(), 0, 0)
		if err != nil {
			t.Error("fakeEmptyStore.ListProjects should succeed")
		}
		_, err = s.ListFileHashes(context.Background(), 1)
		if err != nil {
			t.Error("fakeEmptyStore.ListFileHashes should succeed")
		}
	})
}

// fakeScopeResolver resolves every ref to a fixed scope.
type fakeScopeResolver struct {
	root string
}

func (f *fakeScopeResolver) Resolve(_ context.Context, ref string) (*Scope, error) {
	return &Scope{
		Path:     f.root,
		Identity: ref,
		Source:   "git",
	}, nil
}

// errScopeResolver returns an error for every ref.
type errScopeResolver struct{}

func (errScopeResolver) Resolve(_ context.Context, _ string) (*Scope, error) {
	return nil, errors.New("resolver error")
}

// ---------------------------------------------------------------------------
// Tool interface tests
// ---------------------------------------------------------------------------

// toolFactory creates a Tool for testing; used for table-driven Def/Run checks.
type toolFactory func() Tool

func TestToolDefs(t *testing.T) {
	fs := newFakeSearchStore()
	fs.addProject(&store.Project{Name: "test-proj", Identity: "test-id", Path: "/tmp/test", SourceType: "git", Status: "ready", Model: "test-model"})

	svc := search.NewService(fs, fakeEmbedder{})
	resolver := &fakeScopeResolver{root: "/tmp/test"}

	tools := map[string]toolFactory{
		"semantic_search": func() Tool { return NewSearchTool(svc) },
		"index_status":    func() Tool { return NewIndexStatusTool(fs) },
		"list_projects":   func() Tool { return NewListProjectsTool(fs) },
		"repo_worktrees":  func() Tool { return NewRepoWorktreesTool(resolver) },
		"repo_branches":   func() Tool { return NewRepoBranchesTool(resolver) },
		"repo_status":     func() Tool { return NewRepoStatusTool(resolver) },
	}

	for name, fn := range tools {
		t.Run(name+"/Def", func(t *testing.T) {
			tool := fn()
			def := tool.Def()
			if def.Name != name {
				t.Errorf("Def().Name = %q, want %q", def.Name, name)
			}
			if def.Description == "" {
				t.Error("Def().Description is empty")
			}
			if def.Parameters == nil {
				t.Error("Def().Parameters is nil")
			}
			if typ, ok := def.Parameters["type"]; !ok || typ != "object" {
				t.Errorf("Def().Parameters[\"type\"] = %v, want \"object\"", typ)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// semantic_search tool
// ---------------------------------------------------------------------------

func TestSearchTool_Run_validArgs(t *testing.T) {
	fs := newFakeSearchStore()
	fs.addProject(&store.Project{
		Name: "test-proj", Identity: "test-id",
		Path: "/tmp/test", SourceType: "git", Status: "ready", Model: "test-model", ID: 1,
	})

	svc := search.NewService(fs, fakeEmbedder{})
	tool := NewSearchTool(svc)

	result, err := tool.Run(ctx, `{"query":"find main function","project":"test-proj","top_k":3}`)
	if err != nil {
		t.Fatalf("Run(valid args): %v", err)
	}
	// Should return JSON with results
	if !strings.Contains(result, `"results"`) {
		t.Errorf("result missing 'results' key: %s", result)
	}
	if !strings.Contains(result, `"total"`) {
		t.Errorf("result missing 'total' key: %s", result)
	}
	if !strings.Contains(result, `"project"`) {
		t.Errorf("result missing 'project' key: %s", result)
	}
}

func TestSearchTool_Run_invalidJSON(t *testing.T) {
	fs := newFakeSearchStore()
	svc := search.NewService(fs, fakeEmbedder{})
	tool := NewSearchTool(svc)

	_, err := tool.Run(ctx, `not json`)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "invalid arguments") {
		t.Errorf("error should mention 'invalid arguments', got: %v", err)
	}
}

func TestSearchTool_Run_missingProject(t *testing.T) {
	fs := newFakeSearchStore()
	svc := search.NewService(fs, fakeEmbedder{})
	tool := NewSearchTool(svc)

	_, err := tool.Run(ctx, `{"query":"find main"}`)
	if err == nil {
		t.Fatal("expected error for missing project, got nil")
	}
}

// ---------------------------------------------------------------------------
// index_status tool
// ---------------------------------------------------------------------------

func TestIndexStatusTool_Run_validArgs(t *testing.T) {
	fs := newFakeSearchStore()
	fs.addProject(&store.Project{
		Name: "my-project", Identity: "my-id",
		Path: "/tmp/my-project", SourceType: "git", Status: "ready",
		Model: "test-model", ID: 1,
	})
	fs.fileHashes[1] = map[string]string{"file1.go": "abc123"}

	tool := NewIndexStatusTool(fs)
	result, err := tool.Run(ctx, `{"project":"my-project"}`)
	if err != nil {
		t.Fatalf("Run(valid args): %v", err)
	}
	if !strings.Contains(result, `"name":"my-project"`) {
		t.Errorf("result missing project name: %s", result)
	}
	if !strings.Contains(result, `"status"`) {
		t.Errorf("result missing 'status': %s", result)
	}
}

func TestIndexStatusTool_Run_noProject(t *testing.T) {
	fs := newFakeSearchStore()
	tool := NewIndexStatusTool(fs)

	result, err := tool.Run(ctx, `{}`)
	if err != nil {
		t.Fatalf("Run(empty args) should not return hard error: %v", err)
	}
	// Should return soft error JSON when no project specified
	if !strings.Contains(result, `"error"`) {
		t.Errorf("result should contain error for missing project: %s", result)
	}
}

func TestIndexStatusTool_Run_invalidJSON(t *testing.T) {
	fs := newFakeSearchStore()
	tool := NewIndexStatusTool(fs)

	_, err := tool.Run(ctx, `{{{broken}}`)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestIndexStatusTool_Run_projectNotFound(t *testing.T) {
	fs := newFakeSearchStore()
	tool := NewIndexStatusTool(fs)

	_, err := tool.Run(ctx, `{"project":"nonexistent"}`)
	if err == nil {
		t.Fatal("expected error for nonexistent project, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// list_projects tool
// ---------------------------------------------------------------------------

func TestListProjectsTool_Run_valid(t *testing.T) {
	fs := newFakeSearchStore()
	fs.addProject(&store.Project{
		Name: "proj-a", Identity: "id-a", SourceType: "git", Status: "ready", Model: "m1",
	})
	fs.addProject(&store.Project{
		Name: "proj-b", Identity: "id-b", SourceType: "docs", Status: "indexing", Model: "m2",
	})

	tool := NewListProjectsTool(fs)
	result, err := tool.Run(ctx, `{}`)
	if err != nil {
		t.Fatalf("Run(valid): %v", err)
	}
	if !strings.Contains(result, `"proj-a"`) {
		t.Errorf("result missing 'proj-a': %s", result)
	}
	if !strings.Contains(result, `"proj-b"`) {
		t.Errorf("result missing 'proj-b': %s", result)
	}
	if !strings.Contains(result, `"total":2`) {
		t.Errorf("result should have total=2: %s", result)
	}
}

func TestListProjectsTool_Run_empty(t *testing.T) {
	fs := newFakeSearchStore()
	tool := NewListProjectsTool(fs)
	result, err := tool.Run(ctx, `{}`)
	if err != nil {
		t.Fatalf("Run(empty): %v", err)
	}
	if !strings.Contains(result, `"total":0`) {
		t.Errorf("result should have total=0: %s", result)
	}
}

// ---------------------------------------------------------------------------
// repo_worktrees / repo_branches / repo_status — Def + JSON parsing only
// (Run execution needs real git)
// ---------------------------------------------------------------------------

func TestRepoTools_Run_invalidJSON(t *testing.T) {
	// These tools parse JSON before calling git, so invalid JSON should fail
	// even without a real resolver.
	resolver := errScopeResolver{}
	tools := []Tool{
		NewRepoWorktreesTool(&resolver),
		NewRepoBranchesTool(&resolver),
		NewRepoStatusTool(&resolver),
	}

	for _, tool := range tools {
		t.Run(tool.Def().Name+"/invalidJSON", func(t *testing.T) {
			_, err := tool.Run(ctx, `{{{bad`)
			if err == nil {
				t.Fatal("expected error for invalid JSON, got nil")
			}
			if !strings.Contains(err.Error(), "invalid arguments") {
				t.Errorf("error should mention 'invalid arguments', got: %v", err)
			}
		})
	}
}

func TestRepoTools_Run_missingProject(t *testing.T) {
	resolver := errScopeResolver{}
	tools := []Tool{
		NewRepoWorktreesTool(&resolver),
		NewRepoBranchesTool(&resolver),
		NewRepoStatusTool(&resolver),
	}

	for _, tool := range tools {
		t.Run(tool.Def().Name+"/missingProject", func(t *testing.T) {
			_, err := tool.Run(ctx, `{"project":""}`)
			if err == nil {
				t.Fatal("expected error for missing project, got nil")
			}
		})
	}
}

func TestRepoTools_Run_resolveFailure(t *testing.T) {
	resolver := errScopeResolver{}
	tools := []Tool{
		NewRepoWorktreesTool(&resolver),
		NewRepoBranchesTool(&resolver),
		NewRepoStatusTool(&resolver),
	}

	for _, tool := range tools {
		t.Run(tool.Def().Name, func(t *testing.T) {
			_, err := tool.Run(ctx, `{"project":"some-project"}`)
			if err == nil {
				t.Fatal("expected error for resolve failure, got nil")
			}
			if !strings.Contains(err.Error(), "resolve project") {
				t.Errorf("error should mention 'resolve project', got: %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// truncate helper
// ---------------------------------------------------------------------------

func TestTruncate_short(t *testing.T) {
	got := truncate("hello", 10)
	if got != "hello" {
		t.Errorf("truncate(short) = %q, want %q", got, "hello")
	}
}

func TestTruncate_exact(t *testing.T) {
	got := truncate("hello", 5)
	if got != "hello" {
		t.Errorf("truncate(exact) = %q, want %q", got, "hello")
	}
}

func TestTruncate_long(t *testing.T) {
	got := truncate("hello world foo bar baz", 5)
	want := "hello…"
	if got != want {
		t.Errorf("truncate(long) = %q, want %q", got, want)
	}
}

func TestTruncate_unicode(t *testing.T) {
	got := truncate("héllo wörld", 6)
	want := "héllo …"
	if got != want {
		t.Errorf("truncate(unicode) = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// resolveRoot helper
// ---------------------------------------------------------------------------

func TestResolveRoot_nilResolver(t *testing.T) {
	_, err := resolveRoot(ctx, nil, "my-project")
	if err == nil {
		t.Fatal("expected error for nil resolver, got nil")
	}
	if !strings.Contains(err.Error(), "scope resolver not available") {
		t.Errorf("error should mention resolver: %v", err)
	}
}

func TestResolveRoot_emptyRef(t *testing.T) {
	resolver := &fakeScopeResolver{root: "/tmp"}
	_, err := resolveRoot(ctx, resolver, "")
	if err == nil {
		t.Fatal("expected error for empty ref, got nil")
	}
	if !strings.Contains(err.Error(), "no project specified") {
		t.Errorf("error should mention 'no project specified': %v", err)
	}
}

func TestResolveRoot_resolverError(t *testing.T) {
	resolver := errScopeResolver{}
	_, err := resolveRoot(ctx, resolver, "my-project")
	if err == nil {
		t.Fatal("expected error from resolver, got nil")
	}
}

func TestResolveRoot_noLocalPath(t *testing.T) {
	resolver := &fakeNoPathResolver{}
	_, err := resolveRoot(ctx, resolver, "remote-proj")
	if err == nil {
		t.Fatal("expected error for remote-only project, got nil")
	}
	if !strings.Contains(err.Error(), "no local path") {
		t.Errorf("error should mention 'no local path': %v", err)
	}
}

// fakeNoPathResolver returns a scope without a local path.
type fakeNoPathResolver struct{}

func (f *fakeNoPathResolver) Resolve(_ context.Context, _ string) (*Scope, error) {
	return &Scope{Path: "", Identity: "remote-id", Source: "git"}, nil
}
