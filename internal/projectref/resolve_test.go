package projectref

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/gitenv"
	"github.com/lgldsilva/semidx/internal/store"
)

type memIndex struct {
	store.IndexStore
	projects []store.Project
	listErr  error
}

type directLookupIndex struct {
	store.IndexStore
	project store.Project
}

func (d *directLookupIndex) ListProjects(context.Context, int, int) ([]store.Project, error) {
	return nil, nil
}

func (d *directLookupIndex) GetProject(_ context.Context, name string) (*store.Project, error) {
	if name == d.project.Name {
		return &d.project, nil
	}
	return nil, store.ErrNotFound
}

func (d *directLookupIndex) GetProjectByIdentity(context.Context, string) (*store.Project, error) {
	return nil, store.ErrNotFound
}

func (m *memIndex) ListProjects(context.Context, int, int) ([]store.Project, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.projects, nil
}

func (m *memIndex) GetProject(_ context.Context, name string) (*store.Project, error) {
	for i := range m.projects {
		if m.projects[i].Name == name {
			return &m.projects[i], nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *memIndex) GetProjectByIdentity(_ context.Context, identity string) (*store.Project, error) {
	for i := range m.projects {
		if m.projects[i].Identity == identity {
			return &m.projects[i], nil
		}
	}
	return nil, store.ErrNotFound
}

func TestResolveExactNameViaList(t *testing.T) {
	db := &memIndex{projects: []store.Project{
		{ID: 1, Name: "jackui", Identity: "git:example/jackui", Path: "/data/jackui"},
	}}
	p, err := Resolve(context.Background(), db, "JackUI")
	if err != nil || p == nil || p.Name != "jackui" {
		t.Fatalf("Resolve(JackUI) = %+v, %v", p, err)
	}
}

func TestResolveByIdentityString(t *testing.T) {
	db := &memIndex{projects: []store.Project{
		{ID: 1, Name: "jackui", Identity: "git:example/jackui"},
	}}
	p, err := Resolve(context.Background(), db, "git:example/jackui")
	if err != nil || p == nil || p.Name != "jackui" {
		t.Fatalf("Resolve(identity) = %+v, %v", p, err)
	}
}

func TestResolveInListByPath(t *testing.T) {
	projects := []store.Project{
		{ID: 1, Name: "docs", Identity: "path:/tmp/docs", Path: "/tmp/docs"},
	}
	p, err := ResolveInList(context.Background(), "/tmp/docs", "", projects)
	if err != nil || p == nil || p.Name != "docs" {
		t.Fatalf("ResolveInList(path) = %+v, %v", p, err)
	}
}

func TestUniqueByIdentity(t *testing.T) {
	in := []store.Project{
		{ID: 1, Name: "jackui-a", Identity: "git:example/jackui"},
		{ID: 2, Name: "jackui-b", Identity: "git:example/jackui"},
		{ID: 3, Name: "other", Identity: "git:example/other"},
		{ID: 4, Name: "legacy", Identity: ""},
	}
	out := UniqueByIdentity(in)
	if len(out) != 3 {
		t.Fatalf("UniqueByIdentity len = %d, want 3", len(out))
	}
}

func TestResolveByPathIdentityKey(t *testing.T) {
	dir := t.TempDir()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	db := &memIndex{projects: []store.Project{
		{ID: 1, Name: "docs", Identity: "path:" + abs, Path: abs},
	}}
	p, err := Resolve(context.Background(), db, dir)
	if err != nil || p == nil || p.Name != "docs" {
		t.Fatalf("Resolve(path identity) = %+v, %v", p, err)
	}
}

func TestResolveExplicitPathWinsForSameBasenameProjects(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "one", "app")
	second := filepath.Join(root, "two", "app")
	for _, dir := range []string{first, second} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	db := &memIndex{projects: []store.Project{
		{ID: 1, Name: "app", Identity: "path:" + first, Path: first},
		{ID: 2, Name: "app", Identity: "path:" + second, Path: second},
	}}
	project, err := Resolve(context.Background(), db, second)
	if err != nil || project.ID != 2 {
		t.Fatalf("Resolve(second path) = %+v, %v; want project 2", project, err)
	}
}

func TestResolveTreatsSymlinkAndRealPathAsEquivalent(t *testing.T) {
	root := t.TempDir()
	realPath := filepath.Join(root, "real")
	if err := os.Mkdir(realPath, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasPath := filepath.Join(root, "alias")
	if err := os.Symlink(realPath, aliasPath); err != nil {
		t.Skipf("create symlink: %v", err)
	}

	tests := []struct {
		name        string
		indexedPath string
		ref         string
	}{
		{name: "indexed alias queried by real path", indexedPath: aliasPath, ref: realPath},
		{name: "indexed real path queried by alias", indexedPath: realPath, ref: aliasPath},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := &memIndex{projects: []store.Project{{
				ID: 1, Name: "docs", Identity: "path:" + tt.indexedPath, Path: tt.indexedPath,
			}}}
			project, err := Resolve(context.Background(), db, tt.ref)
			if err != nil || project == nil || project.ID != 1 {
				t.Fatalf("Resolve(%q) = %+v, %v; want equivalent indexed path %q", tt.ref, project, err, tt.indexedPath)
			}
		})
	}
}

func TestResolveRejectsAmbiguousProjectName(t *testing.T) {
	db := &memIndex{projects: []store.Project{
		{ID: 1, Name: "app", Identity: "path:/one/app", Path: "/one/app"},
		{ID: 2, Name: "app", Identity: "path:/two/app", Path: "/two/app"},
	}}
	if _, err := Resolve(context.Background(), db, "app"); err == nil {
		t.Fatal("ambiguous project name should not select the first project")
	}
}

func TestResolveInListByPathIdentityKey(t *testing.T) {
	dir := t.TempDir()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	projects := []store.Project{{ID: 1, Name: "docs", Identity: "path:" + abs, Path: abs}}
	p, err := ResolveInList(context.Background(), dir, "", projects)
	if err != nil || p == nil || p.Name != "docs" {
		t.Fatalf("ResolveInList(path identity) = %+v, %v", p, err)
	}
}

func TestResolveListProjectsError(t *testing.T) {
	db := &memIndex{listErr: context.Canceled, projects: []store.Project{{Name: "jackui"}}}
	if _, err := Resolve(context.Background(), db, "JackUI"); err == nil {
		t.Fatal("expected list error")
	}
}

func TestResolveInListEmptyRef(t *testing.T) {
	if _, err := ResolveInList(context.Background(), "  ", "", nil); err != store.ErrNotFound {
		t.Fatalf("empty ref = %v", err)
	}
}

func TestResolveExactNameViaGetProject(t *testing.T) {
	db := &directLookupIndex{project: store.Project{ID: 1, Name: "alpha", Path: "/tmp/alpha"}}
	p, err := Resolve(context.Background(), db, "alpha")
	if err != nil || p == nil || p.Name != "alpha" {
		t.Fatalf("Resolve(alpha) = %+v, %v", p, err)
	}
}

func TestResolveByIndexedPath(t *testing.T) {
	dir := t.TempDir()
	projects := []store.Project{{ID: 1, Name: "docs", Path: dir}}
	p, err := resolveInList(dir, "", projects)
	if err != nil || p == nil || p.Name != "docs" {
		t.Fatalf("resolveInList(path) = %+v, %v", p, err)
	}
}

func TestEnclosingPicksLongestPrefix(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	projects := []store.Project{
		{ID: 1, Name: "root", Path: root},
		{ID: 2, Name: "other", Path: t.TempDir()},
	}
	if p := Enclosing(sub, projects); p == nil || p.Name != "root" {
		t.Fatalf("Enclosing = %+v, want root", p)
	}
}

func TestEnclosingTreatsSymlinkAndRealPathAsEquivalent(t *testing.T) {
	root := t.TempDir()
	realPath := filepath.Join(root, "real")
	child := filepath.Join(realPath, "pkg")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasPath := filepath.Join(root, "alias")
	if err := os.Symlink(realPath, aliasPath); err != nil {
		t.Skipf("create symlink: %v", err)
	}

	projects := []store.Project{{ID: 1, Name: "root", Path: aliasPath}}
	if project := Enclosing(child, projects); project == nil || project.ID != 1 {
		t.Fatalf("Enclosing(%q) = %+v; want project indexed through %q", child, project, aliasPath)
	}
}

func TestResolveNotFound(t *testing.T) {
	db := &memIndex{projects: []store.Project{{ID: 1, Name: "only"}}}
	if _, err := Resolve(context.Background(), db, "missing"); err != store.ErrNotFound {
		t.Fatalf("Resolve missing = %v", err)
	}
}

func gitInit(t *testing.T, dir string, args ...[]string) {
	t.Helper()
	base := [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@e.st"},
		{"config", "user.name", "t"},
	}
	for _, a := range append(base, args...) {
		cmd := exec.Command("git", append([]string{"-C", dir}, a...)...)
		cmd.Env = append(gitenv.Clean(os.Environ()), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
}

func TestResolveGitRepoPath(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir, []string{"remote", "add", "origin", "git@github.com:acme/app.git"})
	db := &memIndex{projects: []store.Project{{
		ID: 1, Name: "app", Identity: "remote:github.com/acme/app", Path: dir,
	}}}
	p, err := Resolve(context.Background(), db, dir)
	if err != nil || p == nil || p.Name != "app" {
		t.Fatalf("Resolve(git path) = %+v, %v", p, err)
	}
}

func TestResolveInListWithCwdEnclosing(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	projects := []store.Project{{ID: 1, Name: "root", Path: root}}
	p, err := ResolveInList(context.Background(), "missing", sub, projects)
	if err != nil || p == nil || p.Name != "root" {
		t.Fatalf("ResolveInList(cwd enclosing) = %+v, %v", p, err)
	}
}

func TestFindByIdentityEmpty(t *testing.T) {
	projects := []store.Project{{ID: 1, Name: "p", Identity: "git:x"}}
	if findByIdentity(projects, "") != nil {
		t.Fatal("empty identity should not match")
	}
}

func TestResolveEmptyRef(t *testing.T) {
	db := &memIndex{projects: []store.Project{{Name: "p"}}}
	if _, err := Resolve(context.Background(), db, "  "); err != store.ErrNotFound {
		t.Fatalf("empty ref = %v", err)
	}
}

func TestFindByIndexedPathAbsError(t *testing.T) {
	if findByIndexedPath("\000", nil) != nil {
		t.Fatal("expected nil on invalid path")
	}
}

func TestEnclosingSkipsBadProjectPath(t *testing.T) {
	projects := []store.Project{{ID: 1, Name: "bad", Path: "\000"}}
	if Enclosing(t.TempDir(), projects) != nil {
		t.Fatal("expected no match when project path is invalid")
	}
}

func TestResolveInListGitRepoPath(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir, []string{"remote", "add", "origin", "https://github.com/acme/other.git"})
	projects := []store.Project{{
		ID: 1, Name: "other", Identity: "remote:github.com/acme/other", Path: dir,
	}}
	p, err := ResolveInList(context.Background(), dir, "", projects)
	if err != nil || p == nil || p.Name != "other" {
		t.Fatalf("ResolveInList(git path) = %+v, %v", p, err)
	}
}
