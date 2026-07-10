package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/store"
)

type errEmbedder struct{}

func (errEmbedder) ModelInfo(context.Context, string) (*embed.ModelInfo, error) {
	return nil, errors.New("model down")
}
func (errEmbedder) EmbedSingle(context.Context, string, string) ([]float32, error) {
	return nil, errors.New("model down")
}
func (errEmbedder) Embed(context.Context, string, ...string) ([][]float32, error) {
	return nil, errors.New("model down")
}
func (errEmbedder) ListModels(context.Context) ([]string, error) { return nil, nil }

func TestHandleFilesBatchSyncContent(t *testing.T) {
	t.Parallel()
	writeTok := &store.Token{Scopes: []string{"write"}}
	srv := New(&fakeStore{
		token:   writeTok,
		project: &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "push"},
	}, fakeEmbedder{}, nil)

	body := `{"files":[{"path":"main.go","content":"package main\n\nfunc main() {}\n"}],"delete":["old.go"]}`
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/batch?sync=true", "tok", body)
	if rec.Code != 200 {
		t.Fatalf("sync batch = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"indexed":1`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestHandleFilesBatchPreEmbedded(t *testing.T) {
	t.Parallel()
	writeTok := &store.Token{Scopes: []string{"write"}}
	srv := New(&fakeStore{
		token:   writeTok,
		project: &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "push"},
	}, fakeEmbedder{}, nil)

	body := `{"files":[{"path":"a.go","content":"hello","chunks":[{"start_line":1,"end_line":1,"content":"hello","embedding":[1,0,0]}]}],"delete":[]}`
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/batch?sync=true", "tok", body)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"chunks":1`) {
		t.Fatalf("pre-embedded = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleFilesBatchEmptyFile(t *testing.T) {
	t.Parallel()
	writeTok := &store.Token{Scopes: []string{"write"}}
	srv := New(&fakeStore{
		token:   writeTok,
		project: &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "push"},
	}, fakeEmbedder{}, nil)

	body := `{"files":[{"path":"empty.go"}],"delete":[]}`
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/batch?sync=true", "tok", body)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"errors":1`) {
		t.Fatalf("empty file = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleFilesDiff(t *testing.T) {
	t.Parallel()
	writeTok := &store.Token{Scopes: []string{"write"}}
	srv := New(&fakeStore{
		token: writeTok,
		project: &store.Project{ID: 1, Name: "p", Model: "bge-m3"},
		fileHashes: map[string]string{
			"keep.go": "aaa",
			"gone.go": "bbb",
		},
	}, fakeEmbedder{}, nil)

	body := `{"files":{"keep.go":"aaa","new.go":"ccc"}}`
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/diff", "tok", body)
	if rec.Code != 200 {
		t.Fatalf("diff = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"new.go"`) || !strings.Contains(rec.Body.String(), `"gone.go"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestHandleFilesBatchAsyncPush(t *testing.T) {
	t.Parallel()
	writeTok := &store.Token{Scopes: []string{"write"}}
	srv := New(&fakeStore{
		token:   writeTok,
		project: &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "push"},
	}, fakeEmbedder{}, nil)

	body := `{"files":[{"path":"a.go","content":"package a\n"}],"delete":[]}`
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/batch", "tok", body)
	if rec.Code != 202 || !strings.Contains(rec.Body.String(), `"job_id"`) {
		t.Fatalf("async batch = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleFilesBatchInvalidJSON(t *testing.T) {
	t.Parallel()
	writeTok := &store.Token{Scopes: []string{"write"}}
	srv := New(&fakeStore{
		token:   writeTok,
		project: &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "push"},
	}, fakeEmbedder{}, nil)

	rec := do(t, srv, "POST", "/api/v1/projects/p/files/batch?sync=true", "tok", `{bad`)
	if rec.Code != 400 {
		t.Fatalf("invalid json = %d", rec.Code)
	}
}

func TestHandleFilesBatchModelUnavailable(t *testing.T) {
	t.Parallel()
	writeTok := &store.Token{Scopes: []string{"write"}}
	srv := New(&fakeStore{
		token:   writeTok,
		project: &store.Project{ID: 1, Name: "p", Model: "bad", SourceType: "push"},
	}, errEmbedder{}, nil)

	body := `{"files":[{"path":"a.go","content":"x"}],"delete":[]}`
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/batch?sync=true", "tok", body)
	if rec.Code != 502 {
		t.Fatalf("model unavailable = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleFilesBatchEnqueueError(t *testing.T) {
	t.Parallel()
	writeTok := &store.Token{Scopes: []string{"write"}}
	srv := New(&fakeStore{
		token:           writeTok,
		project:         &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "push"},
		enqueueBatchErr: errors.New("queue full"),
	}, fakeEmbedder{}, nil)
	body := `{"files":[{"path":"a.go","content":"x"}],"delete":[]}`
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/batch", "tok", body)
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), "enqueue") {
		t.Fatalf("enqueue err = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestLoadProjectInternalError(t *testing.T) {
	t.Parallel()
	writeTok := &store.Token{Scopes: []string{"write"}}
	srv := New(&fakeStore{
		token:   writeTok,
		project: &store.Project{ID: 1, Name: "p", Model: "bge-m3"},
		getErr:  errors.New("db"),
	}, fakeEmbedder{}, nil)
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/diff", "tok", `{"files":{"a.go":"h"}}`)
	if rec.Code != 500 {
		t.Fatalf("load project err = %d", rec.Code)
	}
}

func TestHandleFilesDiffErrors(t *testing.T) {
	t.Parallel()
	writeTok := &store.Token{Scopes: []string{"write"}}
	srv := New(&fakeStore{
		token:       writeTok,
		project:     &store.Project{ID: 1, Name: "p", Model: "bge-m3"},
		fileHashErr: errors.New("db down"),
	}, fakeEmbedder{}, nil)
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/diff", "tok", `{"files":{"a.go":"h"}}`)
	if rec.Code != 500 {
		t.Fatalf("list hashes err = %d", rec.Code)
	}

	srv2 := New(&fakeStore{
		token:   writeTok,
		project: &store.Project{ID: 1, Name: "p", Model: "bge-m3"},
	}, fakeEmbedder{}, nil)
	rec = do(t, srv2, "POST", "/api/v1/projects/p/files/diff", "tok", `{"files":{"../x":"h"}}`)
	if rec.Code != 400 {
		t.Fatalf("bad path = %d", rec.Code)
	}
}

func TestHandleFilesBatchEnsureChunksError(t *testing.T) {
	t.Parallel()
	writeTok := &store.Token{Scopes: []string{"write"}}
	srv := New(&fakeStore{
		token:     writeTok,
		project:   &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "push"},
		ensureErr: errors.New("ddl failed"),
	}, fakeEmbedder{}, nil)
	body := `{"files":[{"path":"a.go","content":"x"}],"delete":[]}`
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/batch?sync=true", "tok", body)
	if rec.Code != 500 {
		t.Fatalf("ensure err = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleFilesBatchPreEmbeddedUpToDate(t *testing.T) {
	t.Parallel()
	writeTok := &store.Token{Scopes: []string{"write"}}
	srv := New(&fakeStore{
		token:    writeTok,
		project:  &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "push"},
		upToDate: true,
	}, fakeEmbedder{}, nil)
	body := `{"files":[{"path":"a.go","content":"hello","chunks":[{"start_line":1,"end_line":1,"content":"hello","embedding":[1,0,0]}]}],"delete":[]}`
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/batch?sync=true", "tok", body)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"chunks":0`) {
		t.Fatalf("up-to-date = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleFilesBatchSensitivePreEmbedded(t *testing.T) {
	t.Parallel()
	writeTok := &store.Token{Scopes: []string{"write"}}
	srv := New(&fakeStore{
		token:   writeTok,
		project: &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "push"},
	}, fakeEmbedder{}, nil)
	body := `{"files":[{"path":".env","content":"SECRET=x","chunks":[{"start_line":1,"end_line":1,"content":"SECRET=x","embedding":[1,0,0]}]}],"delete":[]}`
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/batch?sync=true", "tok", body)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"errors":1`) {
		t.Fatalf("sensitive = %d body=%s", rec.Code, rec.Body.String())
	}
}
