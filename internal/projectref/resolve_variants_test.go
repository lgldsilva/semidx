package projectref

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

// TestResolveStrategies exercises each resolution path through the public Resolve API.
func TestResolveStrategies(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sub := filepath.Join(root, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	gitDir := t.TempDir()
	gitInit(t, gitDir, []string{"remote", "add", "origin", "git@github.com:acme/app.git"})

	projects := []store.Project{
		{ID: 1, Name: "jackui", Identity: "git:example/jackui", Path: "/data/jackui"},
		{ID: 2, Name: "docs", Identity: "path:" + root, Path: root},
		{ID: 3, Name: "app", Identity: "remote:github.com/acme/app", Path: gitDir},
	}
	db := &memIndex{projects: projects}

	tests := []struct {
		name    string
		ref     string
		want    string
		wantErr error
	}{
		{name: "case_insensitive_name", ref: "JackUI", want: "jackui"},
		{name: "identity_string", ref: "git:example/jackui", want: "jackui"},
		{name: "exact_getproject", ref: "jackui", want: "jackui"},
		{name: "path_identity_key", ref: root, want: "docs"},
		{name: "git_repo_path", ref: gitDir, want: "app"},
		{name: "trimmed_ref", ref: "  jackui  ", want: "jackui"},
		{name: "empty_ref", ref: "  ", wantErr: store.ErrNotFound},
		{name: "missing_ref", ref: "ghost", wantErr: store.ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Resolve(ctx, db, tc.ref)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Resolve(%q) err = %v, want %v", tc.ref, err, tc.wantErr)
				}
				return
			}
			if err != nil || p == nil || p.Name != tc.want {
				t.Fatalf("Resolve(%q) = %+v, %v", tc.ref, p, err)
			}
		})
	}
}

// TestResolveInListStrategies covers list-based resolution branches (remote clients).
func TestResolveInListStrategies(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sub := filepath.Join(root, "nested")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	gitDir := t.TempDir()
	gitInit(t, gitDir, []string{"remote", "add", "origin", "https://github.com/acme/other.git"})

	tests := []struct {
		name     string
		ref      string
		cwd      string
		projects []store.Project
		want     string
		wantErr  error
	}{
		{
			name: "by_name",
			ref:  "docs", want: "docs",
			projects: []store.Project{{Name: "docs", Path: root}},
		},
		{
			name: "by_path_identity",
			ref:  root, want: "docs",
			projects: []store.Project{{Name: "docs", Identity: "path:" + root, Path: root}},
		},
		{
			name: "by_indexed_path",
			ref:  root, want: "plain",
			projects: []store.Project{{Name: "plain", Path: root}},
		},
		{
			name: "by_git_path",
			ref:  gitDir, want: "other",
			projects: []store.Project{{Name: "other", Identity: "remote:github.com/acme/other", Path: gitDir}},
		},
		{
			name: "by_cwd_enclosing",
			ref:  "missing", cwd: sub, want: "docs",
			projects: []store.Project{{Name: "docs", Path: root}},
		},
		{name: "empty_ref", ref: "", wantErr: store.ErrNotFound},
		{
			name: "not_found",
			ref:  "ghost", wantErr: store.ErrNotFound,
			projects: []store.Project{{Name: "docs", Path: root}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := ResolveInList(ctx, tc.ref, tc.cwd, tc.projects)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("ResolveInList(%q,%q) err = %v", tc.ref, tc.cwd, err)
				}
				return
			}
			if err != nil || p == nil || p.Name != tc.want {
				t.Fatalf("ResolveInList(%q,%q) = %+v, %v", tc.ref, tc.cwd, p, err)
			}
		})
	}
}

// TestEnclosingBranches covers prefix vs exact path matches and invalid paths.
func TestEnclosingBranches(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	projects := []store.Project{
		{Name: "root", Path: root},
		{Name: "other", Path: other},
		{Name: "bad", Path: "\x00"},
	}

	tests := []struct {
		name string
		cwd  string
		want string
	}{
		{name: "exact_project_root", cwd: root, want: "root"},
		{name: "exact_abs_cwd", cwd: mustAbs(t, root), want: "root"},
		{name: "nested_subdir", cwd: sub, want: "root"},
		{name: "unrelated_path", cwd: t.TempDir(), want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := Enclosing(tc.cwd, projects)
			got := ""
			if p != nil {
				got = p.Name
			}
			if got != tc.want {
				t.Fatalf("Enclosing(%q) = %q, want %q", tc.cwd, got, tc.want)
			}
		})
	}
}

// TestFindByIndexedPathBranches covers abs errors and per-project path normalization.
func TestFindByIndexedPathBranches(t *testing.T) {
	dir := t.TempDir()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		ref      string
		projects []store.Project
		want     string
	}{
		{name: "invalid_ref", ref: "\x00", want: ""},
		{name: "match_abs", ref: dir, projects: []store.Project{{Name: "docs", Path: abs}}, want: "docs"},
		{name: "skip_bad_project_path", ref: dir, projects: []store.Project{
			{Name: "bad", Path: "\x00"},
			{Name: "good", Path: abs},
		}, want: "good"},
		{name: "no_match", ref: dir, projects: []store.Project{{Name: "else", Path: t.TempDir()}}, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := findByIndexedPath(tc.ref, tc.projects)
			got := ""
			if p != nil {
				got = p.Name
			}
			if got != tc.want {
				t.Fatalf("findByIndexedPath = %q, want %q", got, tc.want)
			}
		})
	}
}

func mustAbs(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
