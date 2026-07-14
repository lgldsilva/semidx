package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/store"
)

func TestSystemDirsBlocked(t *testing.T) {
	if !systemDirs["/"] {
		t.Error("/ should be blocked")
	}
	if !systemDirs["/etc"] {
		t.Error("/etc should be blocked")
	}
	if systemDirs["/home/user/project"] {
		t.Error("/home/user/project should NOT be blocked")
	}
}

// fakeDimsEmbedder returns a fixed ModelInfo (or error) for modelDims tests.
type fakeDimsEmbedder struct {
	dims int
	err  error
}

func (f *fakeDimsEmbedder) ModelInfo(context.Context, string) (*embed.ModelInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &embed.ModelInfo{Name: "m", Dims: f.dims}, nil
}
func (f *fakeDimsEmbedder) Embed(context.Context, string, ...string) ([][]float32, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeDimsEmbedder) EmbedSingle(context.Context, string, string) ([]float32, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeDimsEmbedder) ListModels(context.Context) ([]string, error) { return nil, nil }

func TestModelDims(t *testing.T) {
	ctx := context.Background()

	t.Run("keyword-only uses the fixed bucket without the embedder", func(t *testing.T) {
		d := &deps{keywordOnly: true, emb: &fakeDimsEmbedder{err: errors.New("must not be called")}}
		dims, err := d.modelDims(ctx, "any")
		if err != nil || dims != store.KeywordDims {
			t.Fatalf("modelDims = %d, err %v, want %d", dims, err, store.KeywordDims)
		}
	})

	t.Run("provider dims pass through", func(t *testing.T) {
		d := &deps{emb: &fakeDimsEmbedder{dims: 768}}
		dims, err := d.modelDims(ctx, "nomic-embed-text")
		if err != nil || dims != 768 {
			t.Fatalf("modelDims = %d, err %v, want 768", dims, err)
		}
	})

	t.Run("provider error propagates", func(t *testing.T) {
		d := &deps{emb: &fakeDimsEmbedder{err: errors.New("provider down")}}
		if _, err := d.modelDims(ctx, "m"); err == nil || !strings.Contains(err.Error(), "provider down") {
			t.Fatalf("err = %v, want provider down", err)
		}
	})

	t.Run("zero dims is rejected before touching the store", func(t *testing.T) {
		d := &deps{emb: &fakeDimsEmbedder{dims: 0}}
		_, err := d.modelDims(ctx, "mystery-model")
		if err == nil || !strings.Contains(err.Error(), "semidx models") {
			t.Fatalf("err = %v, want guidance mentioning 'semidx models'", err)
		}
	})
}

func TestDocsFlagHint(t *testing.T) {
	if docsFlagHint(true) != " --docs" {
		t.Errorf("docsFlagHint(true) = %q, want ' --docs'", docsFlagHint(true))
	}
	if docsFlagHint(false) != "" {
		t.Errorf("docsFlagHint(false) = %q, want ''", docsFlagHint(false))
	}
}

func TestApplyBranchSuffix(t *testing.T) {
	t.Run("empty branch is no-op", func(t *testing.T) {
		tgt := indexTarget{
			identity:   "remote:github.com/org/repo",
			name:       "repo",
			sourceType: "git",
		}
		got := applyBranchSuffix(tgt, "")
		if got.identity != "remote:github.com/org/repo" {
			t.Errorf("identity = %q, want unchanged", got.identity)
		}
		if got.name != "repo" {
			t.Errorf("name = %q, want unchanged", got.name)
		}
	})

	t.Run("appends branch to git identity and name", func(t *testing.T) {
		tgt := indexTarget{
			identity:   "remote:github.com/org/repo",
			name:       "repo",
			sourceType: "git",
		}
		got := applyBranchSuffix(tgt, "develop")
		if got.identity != "remote:github.com/org/repo#develop" {
			t.Errorf("identity = %q, want %q", got.identity, "remote:github.com/org/repo#develop")
		}
		if got.name != "repo@develop" {
			t.Errorf("name = %q, want %q", got.name, "repo@develop")
		}
	})

	t.Run("ignored for docs projects", func(t *testing.T) {
		tgt := indexTarget{
			identity:   "path:/some/docs",
			name:       "docs",
			sourceType: "docs",
		}
		got := applyBranchSuffix(tgt, "develop")
		if got.identity != "path:/some/docs" {
			t.Errorf("identity = %q, want unchanged", got.identity)
		}
		if got.name != "docs" {
			t.Errorf("name = %q, want unchanged", got.name)
		}
	})

	t.Run("local git repo", func(t *testing.T) {
		tgt := indexTarget{
			identity:   "local:/home/user/project/.git",
			name:       "project",
			sourceType: "git",
		}
		got := applyBranchSuffix(tgt, "feature/x")
		if got.identity != "local:/home/user/project/.git#feature/x" {
			t.Errorf("identity = %q, want %q", got.identity, "local:/home/user/project/.git#feature/x")
		}
		if got.name != "project@feature/x" {
			t.Errorf("name = %q, want %q", got.name, "project@feature/x")
		}
	})
}
