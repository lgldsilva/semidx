package searchtargets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/pkg/client"
)

// TestResolveProjectsBranches maps each ResolveProjects code path to an explicit case.
func TestResolveProjectsBranches(t *testing.T) {
	ctx := context.Background()
	gitDir := t.TempDir()
	gitInit(t, gitDir, []string{"remote", "add", "origin", "git@github.com:acme/app.git"})
	gitID := gitmeta.Resolve(ctx, gitDir).Identity

	encloseRoot := t.TempDir()
	encloseSub := filepath.Join(encloseRoot, "pkg")
	if err := os.Mkdir(encloseSub, 0o755); err != nil {
		t.Fatal(err)
	}

	baseDB := func() *memDB {
		return &memDB{projects: []store.Project{
			{Name: "alpha", Identity: "git:alpha"},
			{Name: "beta", Identity: "git:beta"},
			{Name: "docs", Path: encloseRoot},
			{Name: "app", Identity: gitID},
		}}
	}

	tests := []struct {
		name      string
		db        *memDB
		project   string
		cwd       string
		wantNames []string
		wantErr   string
	}{
		{
			name: "explicit_name", db: baseDB(), project: "alpha", cwd: "/tmp",
			wantNames: []string{"alpha"},
		},
		{
			name: "git_cwd_identity", db: baseDB(), project: "", cwd: gitDir,
			wantNames: []string{"app"},
		},
		{
			name: "enclosing_path", db: baseDB(), project: "", cwd: encloseSub,
			wantNames: []string{"docs"},
		},
		{
			name: "search_all_deduped", db: baseDB(), project: "", cwd: "/nowhere",
			wantNames: []string{"alpha", "beta", "docs", "app"},
		},
		{
			name: "explicit_not_found", db: baseDB(), project: "ghost", cwd: "/tmp",
			wantErr: "project not found",
		},
		{
			name: "empty_index", db: &memDB{projects: nil}, project: "", cwd: "/tmp",
			wantErr: "no indexed projects",
		},
		{
			name: "list_error", db: &memDB{listErr: errors.New("down")}, project: "", cwd: "/tmp",
			wantErr: "down",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveProjects(ctx, tc.db, tc.project, tc.cwd)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.wantNames) {
				t.Fatalf("len = %d, want %d: %+v", len(got), len(tc.wantNames), got)
			}
			for i, name := range tc.wantNames {
				if got[i].Name != name {
					t.Fatalf("[%d] = %q, want %q", i, got[i].Name, name)
				}
			}
		})
	}
}

func TestResolveProjectsGetwdError(t *testing.T) {
	old := osGetwd
	osGetwd = func() (string, error) { return "", errors.New("no cwd") }
	t.Cleanup(func() { osGetwd = old })

	_, err := ResolveProjects(context.Background(), &memDB{projects: []store.Project{{Name: "p"}}}, "", "")
	if err == nil || err.Error() != "no cwd" {
		t.Fatalf("Getwd error = %v", err)
	}
}

func TestResolveProjectsUsesGetwdWhenCwdEmpty(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	db := &memDB{projects: []store.Project{{Name: "here", Path: wd}}}
	got, err := ResolveProjects(context.Background(), db, "", "")
	if err != nil || len(got) != 1 || got[0].Name != "here" {
		t.Fatalf("ResolveProjects(Getwd) = %+v, %v", got, err)
	}
}

// TestResolveRemoteProjectBranches exercises remote ref resolution strategies.
func TestResolveRemoteProjectBranches(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	lister := &fakeLister{projects: []client.Project{
		{Name: "docs", Identity: "path:" + abs, Path: abs},
		{Name: "svc", Identity: "git:example/svc"},
	}}

	tests := []struct {
		name    string
		ref     string
		want    string
		wantErr string
	}{
		{name: "by_name_insensitive", ref: "Docs", want: "docs"},
		{name: "by_path", ref: dir, want: "docs"},
		{name: "by_identity", ref: "git:example/svc", want: "svc"},
		// An empty ref resolves from the working directory; this test runs
		// outside any indexed project, so it reports that rather than
		// demanding --project.
		{name: "empty_ref", ref: "", wantErr: "could not tell which project"},
		{name: "not_found", ref: "ghost", wantErr: "project not found"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := ResolveRemoteProject(ctx, lister, tc.ref)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || p.Name != tc.want {
				t.Fatalf("ResolveRemoteProject = %+v, %v", p, err)
			}
		})
	}
}

// An omitted --project must resolve to the project whose indexed path encloses
// the working directory, so an agent's first remote call does not have to name
// a project it has no way to know yet.
func TestResolveRemoteProjectResolvesFromCwd(t *testing.T) {
	dir := t.TempDir()
	abs, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	lister := &fakeLister{projects: []client.Project{
		{Name: "docs", Identity: "path:" + abs, Path: abs},
	}}
	t.Chdir(abs)

	p, err := ResolveRemoteProject(context.Background(), lister, "")
	if err != nil || p == nil || p.Name != "docs" {
		t.Fatalf("ResolveRemoteProject(\"\") = %+v, %v; want docs", p, err)
	}
}

// A remote git project can have a custom display name and a server-side clone
// path, so neither name nor path is sufficient to resolve --project ".".
// The normalized origin is the stable locator shared by both checkouts.
func TestResolveRemoteProjectResolvesDotByGitOrigin(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir, []string{"remote", "add", "origin", "https://gitea.example/acme/sdk.git"})
	t.Chdir(dir)

	lister := &fakeLister{projects: []client.Project{{
		Name:       "sdk-published",
		Identity:   "sdk-published",
		SourceType: "git",
		GitURL:     "https://gitea.example/acme/sdk.git",
		Path:       "/server/clones/sdk",
	}}}
	p, err := ResolveRemoteProject(context.Background(), lister, ".")
	if err != nil || p == nil || p.Name != "sdk-published" {
		t.Fatalf("ResolveRemoteProject(.) = %+v, %v; want sdk-published", p, err)
	}
}

func TestResolveRemoteProjectResolvesDotByRedactedSSHOrigin(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir, []string{"remote", "add", "origin", "git@gitea.example:acme/sdk.git"})
	t.Chdir(dir)

	// The server redacts the user from scp-like URLs before returning them.
	lister := &fakeLister{projects: []client.Project{{
		Name:     "sdk-published",
		Identity: "sdk-published",
		GitURL:   "gitea.example:acme/sdk.git",
	}}}
	p, err := ResolveRemoteProject(context.Background(), lister, ".")
	if err != nil || p == nil || p.Name != "sdk-published" {
		t.Fatalf("ResolveRemoteProject(.) with redacted SSH origin = %+v, %v; want sdk-published", p, err)
	}
}

func TestResolveRemoteProjectResolvesDotHTTPSVsSSH(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir, []string{"remote", "add", "origin", "git@gitea.example:acme/sdk.git"})
	t.Chdir(dir)

	lister := &fakeLister{projects: []client.Project{{
		Name:     "sdk-published",
		Identity: "sdk-published",
		GitURL:   "https://gitea.example/acme/sdk.git",
	}}}
	p, err := ResolveRemoteProject(context.Background(), lister, ".")
	if err != nil || p == nil || p.Name != "sdk-published" {
		t.Fatalf("ResolveRemoteProject(.) HTTPS vs SSH = %+v, %v; want sdk-published", p, err)
	}
}

func TestResolveRemoteProjectResolvesDotByRemoteIdentity(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir, []string{"remote", "add", "origin", "https://gitea.example/acme/sdk.git"})
	t.Chdir(dir)
	id := gitmeta.Resolve(context.Background(), dir).Identity

	lister := &fakeLister{projects: []client.Project{{
		Name:     "sdk-published",
		Identity: id,
	}}}
	p, err := ResolveRemoteProject(context.Background(), lister, ".")
	if err != nil || p == nil || p.Name != "sdk-published" {
		t.Fatalf("ResolveRemoteProject(.) by identity = %+v, %v; want sdk-published", p, err)
	}
}

func TestResolveRemoteProjectRefusesAmbiguousGitOrigin(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir, []string{"remote", "add", "origin", "https://gitea.example/acme/sdk.git"})
	t.Chdir(dir)

	lister := &fakeLister{projects: []client.Project{
		{Name: "sdk", GitURL: "https://gitea.example/acme/sdk.git"},
		{Name: "sdk-develop", GitURL: "https://gitea.example/acme/sdk.git"},
	}}
	_, err := ResolveRemoteProject(context.Background(), lister, ".")
	if err == nil {
		t.Fatal("want error when two projects share the same origin")
	}
	for _, name := range []string{"sdk", "sdk-develop"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q should list candidate %q", err, name)
		}
	}
}

// A failed resolution must name the projects the caller may pass instead of
// only reporting that the lookup failed.
func TestResolveRemoteProjectErrorListsCandidates(t *testing.T) {
	lister := &fakeLister{projects: []client.Project{
		{Name: "alpha", Identity: "git:example/alpha"},
		{Name: "beta", Identity: "git:example/beta"},
	}}
	_, err := ResolveRemoteProject(context.Background(), lister, "ghost")
	if err == nil {
		t.Fatal("want error for unknown project")
	}
	for _, want := range []string{"alpha", "beta"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention candidate %q", err, want)
		}
	}
}

// TestSearchLocalBranches verifies identity vs name routing and multi-target fan-out.
func TestSearchLocalBranches(t *testing.T) {
	ctx := context.Background()
	db := &memDB{projects: []store.Project{
		{Name: "with-id", Identity: "git:id", Model: "bge-m3", SourceType: "git"},
		{Name: "legacy", Model: "bge-m3"},
	}}
	req := search.Request{Query: "auth", TopK: 3}

	t.Run("identity_routing", func(t *testing.T) {
		out, err := SearchLocal(ctx, db, stubEmbed{}, []*store.Project{&db.projects[0]}, req, gitmeta.Info{})
		if err != nil || len(out) != 1 || out[0].Name != "with-id" {
			t.Fatalf("identity route = %+v, %v", out, err)
		}
	})

	t.Run("name_routing", func(t *testing.T) {
		out, err := SearchLocal(ctx, db, stubEmbed{}, []*store.Project{&db.projects[1]}, req, gitmeta.Info{})
		if err != nil || len(out) != 1 || out[0].Name != "legacy" {
			t.Fatalf("name route = %+v, %v", out, err)
		}
	})

	t.Run("multiple_targets", func(t *testing.T) {
		targets := []*store.Project{&db.projects[0], &db.projects[1]}
		out, err := SearchLocal(ctx, db, stubEmbed{}, targets, req, gitmeta.Info{})
		if err != nil || len(out) != 2 {
			t.Fatalf("multi = %+v, %v", out, err)
		}
	})

	t.Run("worktree_only_when_identity_matches", func(t *testing.T) {
		db2 := &memDB{projects: []store.Project{{
			Name: "with-id", Identity: "git:id", Model: "bge-m3", SourceType: "git",
		}}}
		db2.usedWorktree.Store(false)
		git := gitmeta.Info{IsGit: true, Identity: "git:other", Toplevel: "/wt"}
		if _, err := SearchLocal(ctx, db2, stubEmbed{}, []*store.Project{&db2.projects[0]}, req, git); err != nil {
			t.Fatal(err)
		}
		if db2.usedWorktree.Load() {
			t.Fatal("worktree must not apply when cwd identity differs")
		}
	})

	t.Run("stops_on_first_search_error", func(t *testing.T) {
		db3 := &memDB{projects: []store.Project{
			{Name: "ok", Model: "bge-m3"},
			{Name: "fail", Model: "bge-m3"},
		}}
		db3.searchErr = errors.New("store down")
		_, err := SearchLocal(ctx, db3, stubEmbed{}, []*store.Project{&db3.projects[0], &db3.projects[1]}, req, gitmeta.Info{})
		if err == nil {
			t.Fatal("expected search error")
		}
	})
}
