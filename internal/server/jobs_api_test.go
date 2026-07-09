package server

import (
	"errors"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func TestHandleGetJobValidation(t *testing.T) {
	t.Parallel()
	readTok := &store.Token{Scopes: []string{"read"}}
	srv := New(&fakeStore{token: readTok}, fakeEmbedder{}, nil)

	rec := do(t, srv, "GET", "/api/v1/jobs/1", "tok", "")
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "project query") {
		t.Fatalf("missing project = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = do(t, srv, "GET", "/api/v1/jobs/x?project=p", "tok", "")
	if rec.Code != 400 {
		t.Fatalf("invalid id = %d", rec.Code)
	}
}

func TestWriteJobForProjectMismatch(t *testing.T) {
	t.Parallel()
	readTok := &store.Token{Scopes: []string{"read"}}
	fs := &fakeStore{
		token: readTok,
		job:   &store.Job{ID: 5, ProjectID: 1, Status: "done", Type: "full"},
		projByID: &store.Project{ID: 1, Name: "real"},
	}
	srv := New(fs, fakeEmbedder{}, nil)
	rec := do(t, srv, "GET", "/api/v1/jobs/5?project=other", "tok", "")
	if rec.Code != 404 {
		t.Fatalf("wrong project = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWriteJobForProjectFailedJob(t *testing.T) {
	t.Parallel()
	readTok := &store.Token{Scopes: []string{"read"}}
	fs := &fakeStore{
		token: readTok,
		job:   &store.Job{ID: 6, ProjectID: 1, Status: "failed", Type: "full", Error: "boom"},
		projByID: &store.Project{ID: 1, Name: "p"},
	}
	srv := New(fs, fakeEmbedder{}, nil)
	rec := do(t, srv, "GET", "/api/v1/jobs/6?project=p", "tok", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"error":"index job failed"`) {
		t.Fatalf("failed job = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWriteJobForProjectStoreError(t *testing.T) {
	t.Parallel()
	readTok := &store.Token{Scopes: []string{"read"}}
	fs := &fakeStore{
		token:  readTok,
		jobErr: errors.New("db"),
	}
	srv := New(fs, fakeEmbedder{}, nil)
	rec := do(t, srv, "GET", "/api/v1/jobs/1?project=p", "tok", "")
	if rec.Code != 500 {
		t.Fatalf("get job err = %d", rec.Code)
	}
}
