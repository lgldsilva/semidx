package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/store"
)

type failModelEmbedder struct{ fakeEmbedder }

func (failModelEmbedder) ModelInfo(context.Context, string) (*embed.ModelInfo, error) {
	return nil, errors.New("model unavailable")
}

func TestRunJobIndexPathSuccess(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := &fakeStore{
		claimJob: &store.Job{ID: 11, Type: "full", ProjectID: 1},
		projByID: &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "path", Path: root},
	}
	srv := New(fs, fakeEmbedder{}, nil)
	if !srv.claimAndRun(context.Background(), "") {
		t.Fatal("expected job to be claimed")
	}
	if !fs.compCalled || fs.compFiles < 1 {
		t.Fatalf("CompleteJob files=%d called=%v", fs.compFiles, fs.compCalled)
	}
}

func TestRunJobProjectNotFound(t *testing.T) {
	t.Parallel()
	fs := &fakeStore{
		claimJob:    &store.Job{ID: 12, Type: "full", ProjectID: 99},
		projByIDErr: store.ErrNotFound,
	}
	srv := New(fs, fakeEmbedder{}, nil)
	if !srv.claimAndRun(context.Background(), "") {
		t.Fatal("expected job to be claimed")
	}
	if !fs.failCalled {
		t.Fatal("expected FailJob")
	}
}

func TestRunJobModelInfoFail(t *testing.T) {
	t.Parallel()
	fs := &fakeStore{
		claimJob: &store.Job{ID: 13, Type: "full", ProjectID: 1},
		projByID: &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "path", Path: t.TempDir()},
	}
	srv := New(fs, failModelEmbedder{}, nil)
	if !srv.claimAndRun(context.Background(), "") {
		t.Fatal("expected job to be claimed")
	}
	if !fs.failCalled {
		t.Fatal("expected FailJob on model info")
	}
}

func TestRunJobEnsureChunksFail(t *testing.T) {
	t.Parallel()
	fs := &fakeStore{
		claimJob:  &store.Job{ID: 14, Type: "full", ProjectID: 1},
		projByID:  &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "path", Path: t.TempDir()},
		ensureErr: errors.New("schema"),
	}
	srv := New(fs, fakeEmbedder{}, nil)
	if !srv.claimAndRun(context.Background(), "") {
		t.Fatal("expected job to be claimed")
	}
	if !fs.failCalled {
		t.Fatal("expected FailJob on ensure chunks")
	}
}

func TestRunBatchJobModelInfoFail(t *testing.T) {
	t.Parallel()
	fs := &fakeStore{
		claimJob: &store.Job{ID: 15, Type: "batch", ProjectID: 1, Payload: `{"files":[{"path":"a.go","content":"package a\n"}]}`},
		projByID: &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "push"},
	}
	srv := New(fs, failModelEmbedder{}, nil)
	if !srv.claimAndRun(context.Background(), "") {
		t.Fatal("expected job to be claimed")
	}
	if !fs.failCalled {
		t.Fatal("expected FailJob on batch model info")
	}
}
