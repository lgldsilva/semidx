package server

import (
	"context"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func TestRunBatchJob(t *testing.T) {
	t.Parallel()
	payload := `{"files":[{"path":"a.go","content":"package a\n\nfunc A(){}\n"}],"delete":["old.go"]}`
	fs := &fakeStore{
		claimJob: &store.Job{ID: 7, Type: "batch", ProjectID: 1, Payload: payload},
		project:  &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "push"},
	}
	srv := New(fs, fakeEmbedder{}, nil)
	if !srv.claimAndRun(context.Background(), "") {
		t.Fatal("expected job to be claimed")
	}
	if !fs.compCalled || fs.compFiles != 1 {
		t.Fatalf("CompleteJob files=%d called=%v", fs.compFiles, fs.compCalled)
	}
}

func TestRunBatchJobBadPayload(t *testing.T) {
	t.Parallel()
	fs := &fakeStore{
		claimJob: &store.Job{ID: 8, Type: "batch", ProjectID: 1, Payload: `{bad`},
		project:  &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "push"},
	}
	srv := New(fs, fakeEmbedder{}, nil)
	if !srv.claimAndRun(context.Background(), "") {
		t.Fatal("expected job to be claimed")
	}
	if !fs.failCalled || fs.failMsg == "" {
		t.Fatalf("FailJob not called: %+v", fs.failMsg)
	}
}

func TestRunJobPushNoPath(t *testing.T) {
	t.Parallel()
	fs := &fakeStore{
		claimJob: &store.Job{ID: 9, Type: "full", ProjectID: 1},
		project:  &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "push", Path: ""},
	}
	srv := New(fs, fakeEmbedder{}, nil)
	if !srv.claimAndRun(context.Background(), "") {
		t.Fatal("expected job to be claimed")
	}
	if !fs.failCalled || !strings.Contains(fs.failMsg, "no indexable source path") {
		t.Fatalf("fail=%q called=%v", fs.failMsg, fs.failCalled)
	}
}
