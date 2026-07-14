package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lgldsilva/semidx/internal/store"
)

func TestSetIndexLimitsAndMountAdmin(t *testing.T) {
	t.Parallel()
	srv := New(&fakeStore{}, fakeEmbedder{}, nil)
	srv.SetIndexLimits(IndexLimits{
		MaxFileSize: 1, MaxChunksPerFile: 2, MaxChunksPerProject: 3, MaxFilesPerProject: 4,
	})
	if srv.indexLimits.MaxFileSize != 1 || srv.indexLimits.MaxFilesPerProject != 4 {
		t.Fatalf("limits not applied: %+v", srv.indexLimits)
	}
	adm, err := srv.MountAdmin(false, "")
	if err != nil {
		t.Fatal(err)
	}
	if adm == nil || srv.admin == nil {
		t.Fatal("expected mounted admin")
	}
	if _, err := srv.MountAdmin(false, "zz"); err == nil {
		t.Fatal("invalid csrf hex should fail")
	}
}

func TestStartWorkersSweepsTempKeys(t *testing.T) {
	t.Parallel()
	data := t.TempDir()
	tmp := filepath.Join(data, "ssh", "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "leftover"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	khd := filepath.Join(data, "ssh", "known_hosts.d")
	if err := os.MkdirAll(khd, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(khd, "keep"), []byte("pin"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := New(&fakeStore{}, fakeEmbedder{}, nil)
	srv.StartWorkers(ctx, 0, data) // n<1 → clamped to 1
	time.Sleep(10 * time.Millisecond)
	cancel()

	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("ssh/tmp should be swept, stat=%v", err)
	}
	if _, err := os.Stat(filepath.Join(khd, "keep")); err != nil {
		t.Fatalf("known_hosts.d must survive sweep: %v", err)
	}
}

func TestProjectStatusMissing(t *testing.T) {
	t.Parallel()
	srv := New(&fakeStore{token: &store.Token{Scopes: []string{"read", "write"}}}, fakeEmbedder{}, nil)
	rec := do(t, srv, "GET", "/api/v1/projects/nope/status", "tok", "")
	if rec.Code != 404 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
