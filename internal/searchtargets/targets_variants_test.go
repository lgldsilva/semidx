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
		{name: "empty_ref", ref: "", wantErr: "required"},
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
		db2.usedWorktree = false
		git := gitmeta.Info{IsGit: true, Identity: "git:other", Toplevel: "/wt"}
		if _, err := SearchLocal(ctx, db2, stubEmbed{}, []*store.Project{&db2.projects[0]}, req, git); err != nil {
			t.Fatal(err)
		}
		if db2.usedWorktree {
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
