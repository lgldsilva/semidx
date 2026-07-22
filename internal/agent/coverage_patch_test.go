// coverage-patch: 2026-07-17
package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"charm.land/fantasy"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/gitenv"
	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/permission"
	"github.com/lgldsilva/semidx/internal/store"
	semidxclient "github.com/lgldsilva/semidx/pkg/client"
)

// indexCapableStore satisfies GetProject* (for path resolution) and the
// IndexStore methods IndexProject walks so PolicyExecute paths can run without
// panicking on a nil embedded interface.
type indexCapableStore struct {
	store.IndexStore
	mu       sync.Mutex
	projects map[string]*store.Project
	byIdent  map[string]*store.Project
	nextID   int
}

func newIndexCapableStore() *indexCapableStore {
	return &indexCapableStore{
		projects: make(map[string]*store.Project),
		byIdent:  make(map[string]*store.Project),
	}
}

func (s *indexCapableStore) add(p *store.Project) {
	s.projects[p.Name] = p
	if p.Identity != "" {
		s.byIdent[p.Identity] = p
	}
}

func (s *indexCapableStore) GetProject(_ context.Context, name string) (*store.Project, error) {
	p, ok := s.projects[name]
	if !ok {
		return nil, store.ErrNotFound
	}
	return p, nil
}

func (s *indexCapableStore) GetProjectByIdentity(_ context.Context, id string) (*store.Project, error) {
	p, ok := s.byIdent[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return p, nil
}

func (s *indexCapableStore) ListProjects(context.Context, int, int) ([]store.Project, error) {
	out := make([]store.Project, 0, len(s.projects))
	for _, p := range s.projects {
		out = append(out, *p)
	}
	return out, nil
}

func (s *indexCapableStore) ListFileHashes(context.Context, int) (map[string]string, error) {
	return map[string]string{}, nil
}

func (s *indexCapableStore) FileUpToDate(context.Context, int, string, string, int) (bool, error) {
	return false, nil
}

func (s *indexCapableStore) UpsertProject(context.Context, string, string, string, int) (int, error) {
	return 1, nil
}

func (s *indexCapableStore) UpsertFile(context.Context, int, string, string, int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	return s.nextID, nil
}

func (s *indexCapableStore) DeleteChunksForFile(context.Context, int, int, int) error {
	return nil
}

func (s *indexCapableStore) InsertChunks(context.Context, int, int, []chunker.Chunk, [][]float32, int) error {
	return nil
}

func (s *indexCapableStore) InsertChunksTextOnly(context.Context, int, int, []chunker.Chunk, int) error {
	return nil
}

func (s *indexCapableStore) DeleteFileByPath(context.Context, int, string) error {
	return nil
}

func (s *indexCapableStore) UpdateProjectStatus(context.Context, int, string) error {
	return nil
}

func (s *indexCapableStore) InsertFileDependencies(context.Context, int, string, []string) error {
	return nil
}

func (s *indexCapableStore) GetProjectCommit(context.Context, int) (string, error) {
	return "", nil
}

func (s *indexCapableStore) UpdateProjectCommit(context.Context, int, string) error {
	return nil
}

func (s *indexCapableStore) FetchGraphPathsBFS(context.Context, int, []string, int) (map[string]int, error) {
	return nil, nil
}

func (s *indexCapableStore) EnsureEmbeddingCacheTable(context.Context, int) error {
	return nil
}

func (s *indexCapableStore) LookupEmbeddingCache(context.Context, []string, string, int) (map[string][]float32, error) {
	return map[string][]float32{}, nil
}

func (s *indexCapableStore) InsertEmbeddingCache(context.Context, []string, string, [][]float32, int) error {
	return nil
}

func (s *indexCapableStore) PruneEmbeddingCache(context.Context, int) (int64, error) {
	return 0, nil
}

// newMockServerClient returns a client pointed at an httptest that serves
// GetProject + EnqueueJob for the given project name.
func newMockServerClient(t *testing.T, projectName string, enqueueOK bool) *semidxclient.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/api/v1/projects/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name": projectName, "model": "m", "status": "ready",
				"source_type": "git", "git_url": "https://example.com/r.git",
			})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/index-jobs"):
			if !enqueueOK {
				http.Error(w, "enqueue denied", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"job_id": 42})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return semidxclient.New(srv.URL, "tok", semidxclient.WithHTTPClient(srv.Client()))
}

func initTinyGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(gitenv.Clean(os.Environ()),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "--no-verify", "-m", "c1")
	return dir
}

// --- legacy action tools: execute + server sync paths ---

func TestIndexWorktreeTool_Run_execute(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := newIndexCapableStore()
	fs.add(&store.Project{ID: 1, Name: "p", Identity: "id-p", Path: dir, SourceType: "git", Model: "m"})
	idx := indexing.NewIndexer(fs, fakeEmbedder{}, 8, indexing.IndexerOpts{Workers: 1})

	tool := NewIndexWorktreeTool(fs, idx, PolicyExecute)
	result, err := tool.Run(ctx, `{"project":"p"}`)
	if err != nil {
		t.Fatalf("Run(execute): %v", err)
	}
	if !strings.Contains(result, `"action":"index"`) {
		t.Errorf("result: %s", result)
	}
	if !strings.Contains(result, `"files_scanned"`) {
		t.Errorf("expected index stats in result: %s", result)
	}
}

func TestReindexProjectTool_Run_executeAndConfirm(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := newIndexCapableStore()
	fs.add(&store.Project{ID: 1, Name: "p", Identity: "id-p", Path: dir, SourceType: "git", Model: "m"})
	idx := indexing.NewIndexer(fs, fakeEmbedder{}, 8, indexing.IndexerOpts{Workers: 1})

	// Confirm policy
	tool := NewReindexProjectTool(fs, idx, PolicyConfirm)
	result, err := tool.Run(ctx, `{"project":"p"}`)
	if err != nil {
		t.Fatalf("Run(confirm): %v", err)
	}
	if !strings.Contains(result, `"confirm_required":true`) {
		t.Errorf("confirm result: %s", result)
	}

	// Execute policy
	tool = NewReindexProjectTool(fs, idx, PolicyExecute)
	result, err = tool.Run(ctx, `{"project":"p","model":"m2"}`)
	if err != nil {
		t.Fatalf("Run(execute): %v", err)
	}
	if !strings.Contains(result, `"action":"reindex"`) || !strings.Contains(result, `"files_scanned"`) {
		t.Errorf("execute result: %s", result)
	}

	// Identity fallback
	result, err = tool.Run(ctx, `{"project":"id-p"}`)
	if err != nil {
		t.Fatalf("Run(identity): %v", err)
	}
	if !strings.Contains(result, `"action":"reindex"`) {
		t.Errorf("identity result: %s", result)
	}
}

func TestServerRepoSyncTool_Run_proposeConfirmExecute(t *testing.T) {
	client := newMockServerClient(t, "srv-proj", true)

	// Propose
	tool := NewServerRepoSyncTool(client, PolicyPropose)
	result, err := tool.Run(ctx, `{"project":"srv-proj","branch":"main"}`)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if !strings.Contains(result, `"proposed":true`) {
		t.Errorf("propose: %s", result)
	}

	// Confirm
	tool = NewServerRepoSyncTool(client, PolicyConfirm)
	result, err = tool.Run(ctx, `{"project":"srv-proj"}`)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if !strings.Contains(result, `"confirm_required":true`) {
		t.Errorf("confirm: %s", result)
	}

	// Execute
	tool = NewServerRepoSyncTool(client, PolicyExecute)
	result, err = tool.Run(ctx, `{"project":"srv-proj"}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(result, `"job_id":42`) {
		t.Errorf("execute: %s", result)
	}

	// Project not found on server
	badClient := semidxclient.New("http://127.0.0.1:1", "tok")
	tool = NewServerRepoSyncTool(badClient, PolicyPropose)
	result, err = tool.Run(ctx, `{"project":"missing"}`)
	if err != nil {
		t.Fatalf("not-found soft: %v", err)
	}
	if !strings.Contains(result, `"error"`) {
		t.Errorf("not-found: %s", result)
	}
}

func TestServerRepoSyncTool_Run_enqueueFails(t *testing.T) {
	client := newMockServerClient(t, "srv-proj", false)
	tool := NewServerRepoSyncTool(client, PolicyExecute)
	_, err := tool.Run(ctx, `{"project":"srv-proj"}`)
	if err == nil {
		t.Fatal("expected hard error when enqueue fails")
	}
	if !strings.Contains(err.Error(), "enqueue") {
		t.Errorf("error: %v", err)
	}
}

// --- fantasy action tools ---

func TestActionTools_fantasyExecuteAndSync(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := newIndexCapableStore()
	fs.add(&store.Project{ID: 1, Name: "p", Identity: "id-p", Path: dir, SourceType: "git", Model: "m"})
	idx := indexing.NewIndexer(fs, fakeEmbedder{}, 8, indexing.IndexerOpts{Workers: 1})
	client := newMockServerClient(t, "srv", true)

	// With client: ActionTools also registers server_repo_sync
	tools := ActionTools(fs, idx, client, PolicyExecute, permission.AllowAll)
	if findTool(tools, "server_repo_sync") == nil {
		t.Fatal("server_repo_sync missing when client is set")
	}
	if findTool(tools, "reindex_project") == nil {
		t.Fatal("reindex_project missing")
	}

	// index_worktree execute
	iw := findTool(tools, "index_worktree")
	resp, err := iw.Run(t.Context(), fantasy.ToolCall{Input: `{"project":"p"}`})
	if err != nil {
		t.Fatalf("index execute: %v", err)
	}
	if resp.IsError || !strings.Contains(resp.Content, `"files_scanned"`) {
		t.Errorf("index execute: isErr=%v content=%s", resp.IsError, resp.Content)
	}

	// reindex_project: not found, remote-only, execute, identity
	ri := findTool(tools, "reindex_project")
	resp, _ = ri.Run(t.Context(), fantasy.ToolCall{Input: `{"project":"nope"}`})
	if !resp.IsError {
		t.Errorf("reindex not found: %s", resp.Content)
	}

	fs.add(&store.Project{ID: 2, Name: "remote", Identity: "id-r", Path: "", SourceType: "git", Model: "m"})
	resp, _ = ri.Run(t.Context(), fantasy.ToolCall{Input: `{"project":"remote"}`})
	if !resp.IsError || !strings.Contains(resp.Content, "no local path") {
		t.Errorf("reindex remote-only: %s", resp.Content)
	}

	resp, err = ri.Run(t.Context(), fantasy.ToolCall{Input: `{"project":"p"}`})
	if err != nil || resp.IsError {
		t.Fatalf("reindex execute: err=%v content=%s", err, resp.Content)
	}
	if !strings.Contains(resp.Content, `"action":"reindex"`) {
		t.Errorf("reindex execute content: %s", resp.Content)
	}

	// server_repo_sync: nil client soft error (direct constructor), then full path
	nilSync := ActionTools(nil, nil, nil, PolicyExecute, nil)
	if len(nilSync) != 0 {
		t.Errorf("no deps should yield 0 action tools, got %d", len(nilSync))
	}

	// Call server_repo_sync via registered tool
	ss := findTool(tools, "server_repo_sync")
	resp, err = ss.Run(t.Context(), fantasy.ToolCall{Input: `{"project":"srv"}`})
	if err != nil || resp.IsError {
		t.Fatalf("server sync execute: err=%v content=%s", err, resp.Content)
	}
	if !strings.Contains(resp.Content, `"job_id":42`) {
		t.Errorf("server sync: %s", resp.Content)
	}

	// Propose mode for fantasy reindex + server sync
	toolsProp := ActionTools(fs, idx, client, PolicyPropose, nil)
	ri2 := findTool(toolsProp, "reindex_project")
	resp, _ = ri2.Run(t.Context(), fantasy.ToolCall{Input: `{"project":"p"}`})
	if !strings.Contains(resp.Content, `"proposed":true`) {
		t.Errorf("reindex propose: %s", resp.Content)
	}
	ss2 := findTool(toolsProp, "server_repo_sync")
	resp, _ = ss2.Run(t.Context(), fantasy.ToolCall{Input: `{"project":"srv"}`})
	if !strings.Contains(resp.Content, `"proposed":true`) {
		t.Errorf("sync propose: %s", resp.Content)
	}

	// Confirm deny for reindex
	toolsDeny := ActionTools(fs, idx, client, PolicyConfirm, permission.DenyAll)
	ri3 := findTool(toolsDeny, "reindex_project")
	resp, _ = ri3.Run(t.Context(), fantasy.ToolCall{Input: `{"project":"p"}`})
	if !strings.Contains(resp.Content, `"approved":false`) {
		t.Errorf("reindex deny: %s", resp.Content)
	}
}

func TestActionTools_fantasyServerSyncErrors(t *testing.T) {
	// Direct fantasy tool with nil client is only registered when client != nil,
	// so exercise via a client that fails GetProject and one that fails enqueue.
	fs := newIndexCapableStore()
	idx := indexing.NewIndexer(fs, fakeEmbedder{}, 8, indexing.IndexerOpts{Workers: 1})

	failGet := semidxclient.New("http://127.0.0.1:1", "tok")
	tools := ActionTools(fs, idx, failGet, PolicyExecute, permission.AllowAll)
	ss := findTool(tools, "server_repo_sync")
	resp, err := ss.Run(t.Context(), fantasy.ToolCall{Input: `{"project":"x"}`})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !resp.IsError || !strings.Contains(resp.Content, "not found on server") {
		t.Errorf("expected not-found soft error: %s", resp.Content)
	}

	failEnqueue := newMockServerClient(t, "p", false)
	tools = ActionTools(fs, idx, failEnqueue, PolicyExecute, permission.AllowAll)
	ss = findTool(tools, "server_repo_sync")
	resp, err = ss.Run(t.Context(), fantasy.ToolCall{Input: `{"project":"p"}`})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !resp.IsError || !strings.Contains(resp.Content, "enqueue") {
		t.Errorf("expected enqueue soft error: %s", resp.Content)
	}
}

func TestIndexWorktreeF_executeAndIndexError(t *testing.T) {
	// Execute with a path that exists but Indexer may still succeed on empty dirs.
	dir := t.TempDir()
	fs := newIndexCapableStore()
	fs.add(&store.Project{ID: 1, Name: "p", Identity: "id", Path: dir, SourceType: "git", Model: "m"})
	idx := indexing.NewIndexer(fs, fakeEmbedder{}, 8, indexing.IndexerOpts{Workers: 1})
	tools := ActionTools(fs, idx, nil, PolicyExecute, permission.AllowAll)
	iw := findTool(tools, "index_worktree")
	resp, err := iw.Run(t.Context(), fantasy.ToolCall{Input: `{"project":"p","path":` + jsonQuote(dir) + `}`})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.IsError {
		t.Errorf("unexpected error: %s", resp.Content)
	}
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// --- legacy + fantasy repo tools success paths (real git) ---

func TestRepoTools_Run_success(t *testing.T) {
	dir := initTinyGitRepo(t)
	resolver := &fakeScopeResolver{root: dir}

	// worktrees
	wt := NewRepoWorktreesTool(resolver)
	out, err := wt.Run(ctx, `{"project":"p"}`)
	if err != nil {
		t.Fatalf("worktrees: %v", err)
	}
	if !strings.Contains(out, `"worktrees"`) || !strings.Contains(out, `"total"`) {
		t.Errorf("worktrees: %s", out)
	}

	// branches
	br := NewRepoBranchesTool(resolver)
	out, err = br.Run(ctx, `{"project":"p","remote":true}`)
	if err != nil {
		t.Fatalf("branches: %v", err)
	}
	if !strings.Contains(out, `"branches"`) {
		t.Errorf("branches: %s", out)
	}

	// status
	st := NewRepoStatusTool(resolver)
	out, err = st.Run(ctx, `{"project":"p"}`)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, `"current_branch"`) || !strings.Contains(out, `"head"`) {
		t.Errorf("status: %s", out)
	}
}

func TestRepoTools_fantasy_success(t *testing.T) {
	dir := initTinyGitRepo(t)
	resolver := &fakeScopeResolver{root: dir}
	tools := ReadTools(nil, nil, resolver)

	for _, name := range []string{"repo_worktrees", "repo_branches", "repo_status"} {
		tool := findTool(tools, name)
		if tool == nil {
			t.Fatalf("%s missing", name)
		}
		input := `{"project":"p"}`
		if name == "repo_branches" {
			input = `{"project":"p","remote":true}`
		}
		resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: input})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if resp.IsError {
			t.Errorf("%s error: %s", name, resp.Content)
		}
	}
}

func TestRepoTools_Run_listFails(t *testing.T) {
	// Non-git directory → repotools returns error after resolve succeeds.
	dir := t.TempDir()
	resolver := &fakeScopeResolver{root: dir}
	for _, tool := range []Tool{
		NewRepoWorktreesTool(resolver),
		NewRepoBranchesTool(resolver),
		NewRepoStatusTool(resolver),
	} {
		_, err := tool.Run(ctx, `{"project":"p"}`)
		if err == nil {
			t.Errorf("%s: expected git failure on non-repo", tool.Def().Name)
		}
	}
}

func TestRepoTools_fantasy_listFails(t *testing.T) {
	dir := t.TempDir()
	resolver := &fakeScopeResolver{root: dir}
	tools := ReadTools(nil, nil, resolver)
	for _, name := range []string{"repo_worktrees", "repo_branches", "repo_status"} {
		tool := findTool(tools, name)
		resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: `{"project":"p"}`})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !resp.IsError {
			t.Errorf("%s: expected soft error on non-repo, got %s", name, resp.Content)
		}
	}
}

func TestListProjectsTool_Run_listError(t *testing.T) {
	tool := NewListProjectsTool(fakeListErrStore{})
	_, err := tool.Run(ctx, `{}`)
	if err == nil {
		t.Fatal("expected list projects error")
	}
}

func TestSameGitRepo_emptyIdentity(t *testing.T) {
	if sameGitRepo(ctx, t.TempDir(), "") {
		t.Error("empty identity must be false")
	}
}

func TestResolveRegisteredPath_remoteOnlyAndModelDefault(t *testing.T) {
	fs := newFakeSearchStore()
	fs.addProject(&store.Project{Name: "remote", Identity: "r", Path: "", Model: "m"})
	_, _, _, err := resolveRegisteredPath(ctx, fs, indexWorktreeArgs{Project: "remote"})
	if err == nil || !strings.Contains(err.Error(), "no local path") {
		t.Errorf("remote-only: %v", err)
	}

	dir := t.TempDir()
	fs.addProject(&store.Project{Name: "p", Identity: "id", Path: dir, Model: "default-m"})
	p, path, model, err := resolveRegisteredPath(ctx, fs, indexWorktreeArgs{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "p" || model != "default-m" || path == "" {
		t.Errorf("got p=%v path=%q model=%q", p, path, model)
	}
}
