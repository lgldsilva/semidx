package searchtargets

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/gitenv"
	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/pkg/client"
)

type memDB struct {
	store.IndexStore
	projects     []store.Project
	listErr      error
	searchErr    error
	usedWorktree atomic.Bool
}

func (m *memDB) GetProject(_ context.Context, name string) (*store.Project, error) {
	for i := range m.projects {
		if m.projects[i].Name == name {
			return &m.projects[i], nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *memDB) GetProjectByIdentity(_ context.Context, id string) (*store.Project, error) {
	for i := range m.projects {
		if m.projects[i].Identity == id {
			return &m.projects[i], nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *memDB) ListProjects(context.Context, int, int) ([]store.Project, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.projects, nil
}

type fakeLister struct {
	projects []client.Project
	err      error
}

func (f *fakeLister) ListProjects(context.Context) ([]client.Project, error) {
	return f.projects, f.err
}

type stubEmbed struct{ embed.Embedder }

func (stubEmbed) ModelInfo(context.Context, string) (*embed.ModelInfo, error) {
	return &embed.ModelInfo{Name: "bge-m3", Dims: 3}, nil
}
func (stubEmbed) EmbedSingle(context.Context, string, string) ([]float32, error) {
	return []float32{1, 0, 0}, nil
}

func (m *memDB) SearchSimilar(context.Context, int, []float32, int, int) ([]store.SearchResult, error) {
	if m.searchErr != nil {
		return nil, m.searchErr
	}
	return []store.SearchResult{{FilePath: "a.go", Content: "hit", Score: 0.9}}, nil
}

func (m *memDB) SearchSimilarKeywords(context.Context, int, string, int, int) ([]store.SearchResult, error) {
	if m.searchErr != nil {
		return nil, m.searchErr
	}
	return []store.SearchResult{{FilePath: "a.go", Content: "hit", Score: 0.5}}, nil
}

func (m *memDB) SearchSimilarWorktree(context.Context, int, []float32, int, int, string) ([]store.SearchResult, error) {
	m.usedWorktree.Store(true)
	return []store.SearchResult{{FilePath: "wt.go", Content: "hit", Score: 0.9}}, nil
}

func (m *memDB) SearchSimilarKeywordsWorktree(ctx context.Context, projectID int, queryText string, dims, topK int, worktree string) ([]store.SearchResult, error) {
	m.usedWorktree.Store(true)
	if m.searchErr != nil {
		return nil, m.searchErr
	}
	return []store.SearchResult{{FilePath: "wt.go", Content: "hit", Score: 0.5}}, nil
}

func (m *memDB) FetchGraphPathsBFS(ctx context.Context, projectID int, seedPaths []string, maxDepth int) (map[string]int, error) {
	return nil, nil
}

func (m *memDB) GetProjectCommit(ctx context.Context, projectID int) (string, error) {
	return "", nil
}

func (m *memDB) UpdateProjectCommit(ctx context.Context, projectID int, commitSHA string) error {
	return nil
}

func TestResolveProjectsByName(t *testing.T) {
	db := &memDB{projects: []store.Project{{Name: "jackui", Identity: "git:x"}}}
	got, err := ResolveProjects(context.Background(), db, "jackui", "/tmp")
	if err != nil || len(got) != 1 || got[0].Name != "jackui" {
		t.Fatalf("ResolveProjects = %+v, %v", got, err)
	}
}

func TestResolveProjectsAllUnique(t *testing.T) {
	db := &memDB{projects: []store.Project{
		{Name: "a", Identity: "git:a"},
		{Name: "b", Identity: "git:b"},
	}}
	got, err := ResolveProjects(context.Background(), db, "", "/nowhere")
	if err != nil || len(got) != 2 {
		t.Fatalf("ResolveProjects all = %+v, %v", got, err)
	}
}

func TestResolveRemoteProject(t *testing.T) {
	l := &fakeLister{projects: []client.Project{{Name: "jackui", Identity: "git:x"}}}
	p, err := ResolveRemoteProject(context.Background(), l, "JackUI")
	if err != nil || p.Name != "jackui" {
		t.Fatalf("ResolveRemoteProject = %+v, %v", p, err)
	}
}

func TestResolveRemoteProjectRequiresRef(t *testing.T) {
	if _, err := ResolveRemoteProject(context.Background(), &fakeLister{}, ""); err == nil {
		t.Fatal("expected error for empty ref")
	}
}

func TestSearchLocalUsesIdentity(t *testing.T) {
	db := &memDB{projects: []store.Project{{Name: "app", Identity: "git:app", Model: "bge-m3"}}}
	out, err := SearchLocal(context.Background(), db, stubEmbed{}, []*store.Project{&db.projects[0]},
		search.Request{Query: "q", TopK: 5}, gitmeta.Info{})
	if err != nil || len(out) != 1 || out[0].Resp.Results[0].FilePath != "a.go" {
		t.Fatalf("SearchLocal = %+v, %v", out, err)
	}
}

func TestFromClientProjects(t *testing.T) {
	out := FromClientProjects([]client.Project{{Name: "p", Identity: "git:p", Path: "/p"}})
	if len(out) != 1 || out[0].Identity != "git:p" {
		t.Fatalf("FromClientProjects = %+v", out)
	}
}

func TestResolveRemoteProjectListError(t *testing.T) {
	l := &fakeLister{err: errors.New("down")}
	if _, err := ResolveRemoteProject(context.Background(), l, "p"); err == nil {
		t.Fatal("expected list error")
	}
}

func gitInit(t *testing.T, dir string, args ...[]string) {
	t.Helper()
	base := [][]string{{"init", "-q"}, {"config", "user.email", "t@e.st"}, {"config", "user.name", "t"}}
	for _, a := range append(base, args...) {
		cmd := exec.Command("git", append([]string{"-C", dir}, a...)...)
		cmd.Env = append(gitenv.Clean(os.Environ()), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
}

func TestResolveProjectsByGitCwd(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir, []string{"remote", "add", "origin", "git@github.com:acme/app.git"})
	info := gitmeta.Resolve(context.Background(), dir)
	db := &memDB{projects: []store.Project{{Name: "app", Identity: info.Identity}}}
	got, err := ResolveProjects(context.Background(), db, "", dir)
	if err != nil || len(got) != 1 || got[0].Name != "app" {
		t.Fatalf("ResolveProjects git cwd = %+v, %v", got, err)
	}
}

func TestResolveProjectsListError(t *testing.T) {
	db := &memDB{listErr: errors.New("down")}
	if _, err := ResolveProjects(context.Background(), db, "", "/nowhere"); err == nil {
		t.Fatal("expected list error")
	}
}

func TestResolveProjectsEnclosingCwd(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	db := &memDB{projects: []store.Project{{Name: "root", Path: root}}}
	got, err := ResolveProjects(context.Background(), db, "", sub)
	if err != nil || len(got) != 1 || got[0].Name != "root" {
		t.Fatalf("ResolveProjects enclosing = %+v, %v", got, err)
	}
}

func TestResolveProjectsNamedNotFound(t *testing.T) {
	db := &memDB{projects: []store.Project{{Name: "other"}}}
	if _, err := ResolveProjects(context.Background(), db, "ghost", "/tmp"); err == nil {
		t.Fatal("expected not found")
	}
}

func TestResolveProjectsEmptyIndex(t *testing.T) {
	db := &memDB{projects: []store.Project{}}
	if _, err := ResolveProjects(context.Background(), db, "", "/nowhere"); err == nil {
		t.Fatal("expected empty index error")
	}
}

func TestSearchLocalSearchError(t *testing.T) {
	db := &memDB{projects: []store.Project{{Name: "app", Model: "bge-m3"}}}
	db.searchErr = errors.New("store down")
	if _, err := SearchLocal(context.Background(), db, stubEmbed{}, []*store.Project{&db.projects[0]},
		search.Request{Query: "q", TopK: 5}, gitmeta.Info{}); err == nil {
		t.Fatal("expected search error")
	}
}

func TestResolveProjectsByExplicitRef(t *testing.T) {
	db := &memDB{projects: []store.Project{{Name: "docs", Identity: "path:/tmp/docs", Path: "/tmp/docs"}}}
	got, err := ResolveProjects(context.Background(), db, "docs", "/nowhere")
	if err != nil || len(got) != 1 || got[0].Name != "docs" {
		t.Fatalf("ResolveProjects ref = %+v, %v", got, err)
	}
}

func TestResolveRemoteProjectNotFound(t *testing.T) {
	l := &fakeLister{projects: []client.Project{{Name: "other", Identity: "git:x"}}}
	if _, err := ResolveRemoteProject(context.Background(), l, "ghost"); err == nil {
		t.Fatal("expected not found")
	}
}

func TestSearchLocalUsesProjectNameWhenNoIdentity(t *testing.T) {
	db := &memDB{projects: []store.Project{{Name: "legacy", Model: "bge-m3"}}}
	out, err := SearchLocal(context.Background(), db, stubEmbed{}, []*store.Project{&db.projects[0]},
		search.Request{Query: "q", TopK: 5}, gitmeta.Info{})
	if err != nil || len(out) != 1 {
		t.Fatalf("SearchLocal name-only = %+v, %v", out, err)
	}
}

func TestResolveProjectsGitMissFallsThrough(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir, []string{"remote", "add", "origin", "git@github.com:acme/app.git"})
	db := &memDB{projects: []store.Project{{Name: "docs", Path: dir}}}
	got, err := ResolveProjects(context.Background(), db, "", dir)
	if err != nil || len(got) != 1 || got[0].Name != "docs" {
		t.Fatalf("enclosing after git miss = %+v, %v", got, err)
	}
}

func TestSearchLocalWorktreeFilter(t *testing.T) {
	db := &memDB{projects: []store.Project{{
		Name: "app", Identity: "git:app", Model: "bge-m3", SourceType: "git",
	}}}
	out, err := SearchLocal(context.Background(), db, stubEmbed{}, []*store.Project{&db.projects[0]},
		search.Request{Query: "worktree scoped search", TopK: 5}, gitmeta.Info{IsGit: true, Identity: "git:app", Toplevel: "/wt"})
	if err != nil || len(out) != 1 {
		t.Fatalf("SearchLocal worktree = %+v, %v", out, err)
	}
	if !db.usedWorktree.Load() {
		t.Fatal("expected worktree-scoped search")
	}
}
