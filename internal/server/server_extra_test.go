package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/jwtauth"
	"github.com/lgldsilva/semidx/internal/store"
)

// --- additional fakeStore methods (extend, not rewrite) ---

func (f *fakeStore) CountTokens(context.Context) (int, error) {
	return f.tokenCount, f.countTokErr
}

func (f *fakeStore) CreateToken(_ context.Context, name, hash string, scopes []string) (int, error) {
	if f.createTokErr != nil {
		return 0, f.createTokErr
	}
	f.lastTokName, f.lastTokHash, f.lastTokScopes = name, hash, scopes
	return 1, nil
}

func (f *fakeStore) GetProjectByID(_ context.Context, id int) (*store.Project, error) {
	if f.projByIDErr != nil {
		return nil, f.projByIDErr
	}
	if f.projByID == nil {
		return nil, store.ErrNotFound
	}
	return f.projByID, nil
}

func (f *fakeStore) ClaimJob(context.Context) (*store.Job, error) {
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	j := f.claimJob
	f.claimJob = nil // return it at most once so the worker's drain loop terminates
	return j, nil
}

func (f *fakeStore) CompleteJob(_ context.Context, _, filesIndexed, chunksCreated, deletedFiles, errorCount int) error {
	f.compCalled = true
	f.compFiles, f.compChunks = filesIndexed, chunksCreated
	f.compDeleted, f.compErrors = deletedFiles, errorCount
	return nil
}

func (f *fakeStore) EnqueueBatchJob(_ context.Context, _ int, _ string) (int, error) {
	return f.enqueuedID, f.enqueueErr
}

func (f *fakeStore) FailJob(_ context.Context, _ int, msg string) error {
	f.failCalled = true
	f.failMsg = msg
	if f.failCh != nil {
		f.failCh <- msg
	}
	return nil
}

func (f *fakeStore) EnsureChunksTable(context.Context, int) error           { return f.ensureErr }
func (f *fakeStore) DeleteFileByPath(context.Context, int, string) error    { return nil }
func (f *fakeStore) UpdateProjectStatus(context.Context, int, string) error { return nil }
func (f *fakeStore) UpsertFile(context.Context, int, string, string, int) (int, error) {
	return 1, nil
}
func (f *fakeStore) FileUpToDate(context.Context, int, string, string, int) (bool, error) {
	return false, nil
}
func (f *fakeStore) DeleteChunksForFile(context.Context, int, int, int) error { return nil }
func (f *fakeStore) InsertChunks(context.Context, int, int, []chunker.Chunk, [][]float32, int) error {
	return nil
}
func (f *fakeStore) InsertChunksTextOnly(context.Context, int, int, []chunker.Chunk, int) error {
	return nil
}

// cfgEmbedder is a configurable embedder: ModelInfo can fail, Embed returns
// dims-wide vectors for each input.
type cfgEmbedder struct {
	embed.Embedder
	modelInfoErr error
	dims         int
}

func (e cfgEmbedder) ModelInfo(_ context.Context, m string) (*embed.ModelInfo, error) {
	if e.modelInfoErr != nil {
		return nil, e.modelInfoErr
	}
	d := e.dims
	if d == 0 {
		d = 3
	}
	return &embed.ModelInfo{Name: m, Dims: d}, nil
}

func (e cfgEmbedder) Embed(_ context.Context, _ string, inputs ...string) ([][]float32, error) {
	d := e.dims
	if d == 0 {
		d = 3
	}
	out := make([][]float32, len(inputs))
	for i := range inputs {
		out[i] = make([]float32, d)
		out[i][0] = 1
	}
	return out, nil
}

// --- EnsureBootstrapToken ---

func TestEnsureBootstrapToken(t *testing.T) {
	ctx := context.Background()

	t.Run("empty server generates a random admin token", func(t *testing.T) {
		fs := &fakeStore{tokenCount: 0}
		tok, err := New(fs, fakeEmbedder{}, nil).EnsureBootstrapToken(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(tok, "semidx_") {
			t.Errorf("generated token %q lacks prefix", tok)
		}
		if fs.lastTokName != "bootstrap-admin" || len(fs.lastTokScopes) != 1 || fs.lastTokScopes[0] != "admin" {
			t.Errorf("token created with name=%q scopes=%v", fs.lastTokName, fs.lastTokScopes)
		}
		if fs.lastTokHash != HashToken(tok) {
			t.Error("stored hash must be the hash of the returned plaintext")
		}
	})

	t.Run("uses provided env token", func(t *testing.T) {
		fs := &fakeStore{tokenCount: 0}
		tok, err := New(fs, fakeEmbedder{}, nil).EnsureBootstrapToken(ctx, "fixed-token")
		if err != nil || tok != "fixed-token" {
			t.Fatalf("EnsureBootstrapToken = %q, %v; want fixed-token", tok, err)
		}
		if fs.lastTokHash != HashToken("fixed-token") {
			t.Error("stored hash mismatch for env token")
		}
	})

	t.Run("returns empty when tokens already exist", func(t *testing.T) {
		fs := &fakeStore{tokenCount: 5}
		tok, err := New(fs, fakeEmbedder{}, nil).EnsureBootstrapToken(ctx, "x")
		if err != nil || tok != "" {
			t.Fatalf("EnsureBootstrapToken = %q, %v; want empty", tok, err)
		}
		if fs.lastTokName != "" {
			t.Error("CreateToken should not be called when tokens exist")
		}
	})

	t.Run("propagates CountTokens error", func(t *testing.T) {
		fs := &fakeStore{countTokErr: errors.New("db down")}
		if _, err := New(fs, fakeEmbedder{}, nil).EnsureBootstrapToken(ctx, ""); err == nil {
			t.Error("expected error from CountTokens")
		}
	})

	t.Run("propagates CreateToken error", func(t *testing.T) {
		fs := &fakeStore{tokenCount: 0, createTokErr: errors.New("insert failed")}
		if _, err := New(fs, fakeEmbedder{}, nil).EnsureBootstrapToken(ctx, "t"); err == nil {
			t.Error("expected error from CreateToken")
		}
	})
}

// --- EnableJWT / MountAdmin / Run ---

func TestEnableJWTError(t *testing.T) {
	srv := New(&fakeStore{}, fakeEmbedder{}, nil)
	if err := srv.EnableJWT(""); err == nil {
		t.Error("EnableJWT(\"\") should error on empty secret")
	}
	if err := srv.EnableJWT("good-secret"); err != nil {
		t.Errorf("EnableJWT(non-empty) = %v, want nil", err)
	}
}

func TestMountAdmin(t *testing.T) {
	srv := New(&fakeStore{}, fakeEmbedder{}, nil)
	if err := srv.MountAdmin(false, ""); err != nil {
		t.Fatalf("MountAdmin = %v", err)
	}
	// The /admin/ route is now wired; an unauthenticated hit is handled by the
	// admin UI (a redirect to login), not a 404 from the API mux.
	rec := do(t, srv, "GET", "/admin/", "", "")
	if rec.Code == http.StatusNotFound {
		t.Errorf("/admin/ returned 404; admin handler was not mounted")
	}
}

func TestRun(t *testing.T) {
	t.Run("shuts down cleanly when context is cancelled", func(t *testing.T) {
		srv := New(&fakeStore{}, fakeEmbedder{}, nil)
		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() { errCh <- srv.Run(ctx, "127.0.0.1:0") }()
		time.Sleep(50 * time.Millisecond) // let ListenAndServe start
		cancel()
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("Run returned %v after cancel, want nil", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not return after context cancel")
		}
	})

	t.Run("returns error on an unusable address", func(t *testing.T) {
		srv := New(&fakeStore{}, fakeEmbedder{}, nil)
		err := srv.Run(context.Background(), "127.0.0.1:999999") // invalid port
		if err == nil {
			t.Error("Run should return an error for an invalid address")
		}
	})
}

// --- handleFilesBatch full flows ---

func TestHandleFilesBatchFull(t *testing.T) {
	writeTok := &store.Token{Scopes: []string{"write"}}
	fs := &fakeStore{token: writeTok, project: &store.Project{ID: 1, Name: "p", Model: "m"}}
	srv := New(fs, cfgEmbedder{}, nil)

	body := `{"files":[{"path":"a.go","content":"package main\n\nfunc main() {}\n"}],"delete":["gone.go"]}`
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/batch?sync=true", "tok", body)
	if rec.Code != 200 {
		t.Fatalf("batch = %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"indexed":1`) {
		t.Errorf("expected one indexed file; body %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"deleted":1`) {
		t.Errorf("expected one deletion; body %s", rec.Body.String())
	}
}

func TestHandleFilesBatchModelUnavailable(t *testing.T) {
	writeTok := &store.Token{Scopes: []string{"write"}}
	fs := &fakeStore{token: writeTok, project: &store.Project{ID: 1, Name: "p", Model: "m"}}
	srv := New(fs, cfgEmbedder{modelInfoErr: errors.New("ollama down")}, nil)
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/batch?sync=true", "tok", `{"files":[]}`)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("model unavailable = %d, want 502", rec.Code)
	}
}

func TestHandleFilesBatchEnsureError(t *testing.T) {
	writeTok := &store.Token{Scopes: []string{"write"}}
	fs := &fakeStore{token: writeTok, project: &store.Project{ID: 1, Name: "p", Model: "m"}, ensureErr: errors.New("no table")}
	srv := New(fs, cfgEmbedder{}, nil)
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/batch?sync=true", "tok", `{"files":[]}`)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("ensure table error = %d, want 500", rec.Code)
	}
}

func TestHandleFilesBatchEmptyContent(t *testing.T) {
	// Empty content produces no chunks; the indexer returns no error, so the file
	// counts as "indexed" with zero chunks (no per-file error).
	writeTok := &store.Token{Scopes: []string{"write"}}
	fs := &fakeStore{token: writeTok, project: &store.Project{ID: 1, Name: "p", Model: "m"}}
	srv := New(fs, cfgEmbedder{}, nil)
	body := `{"files":[{"path":"empty.go","content":"   "}]}`
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/batch?sync=true", "tok", body)
	if rec.Code != 200 {
		t.Fatalf("batch = %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"chunks":0`) || !strings.Contains(rec.Body.String(), `"errors":0`) {
		t.Errorf("empty file should yield zero chunks and no errors; body %s", rec.Body.String())
	}
}

func TestHandleFilesBatchPreEmbedded(t *testing.T) {
	// A file carrying pre-computed chunks (client embedded) is stored directly,
	// bypassing the server-side chunk+embed pipeline. cfgEmbedder's default is 3
	// dims, so the embedding must have length 3 to pass validation.
	writeTok := &store.Token{Scopes: []string{"write"}}
	fs := &fakeStore{token: writeTok, project: &store.Project{ID: 1, Name: "p", Model: "m"}}
	srv := New(fs, cfgEmbedder{}, nil)
	body := `{"files":[{"path":"a.go","content":"x","chunks":[{"start_line":1,"end_line":2,"content":"x","embedding":[0.1,0.2,0.3]}]}]}`
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/batch?sync=true", "tok", body)
	if rec.Code != 200 {
		t.Fatalf("batch = %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"indexed":1`) || !strings.Contains(rec.Body.String(), `"chunks":1`) {
		t.Errorf("pre-embedded file should index with one chunk; body %s", rec.Body.String())
	}
}

func TestHandleFilesBatchPreEmbeddedDimMismatch(t *testing.T) {
	// An embedding whose length differs from the model's dims is rejected by
	// indexPreEmbedded, so the file is counted as a per-file error (not a 500).
	writeTok := &store.Token{Scopes: []string{"write"}}
	fs := &fakeStore{token: writeTok, project: &store.Project{ID: 1, Name: "p", Model: "m"}}
	srv := New(fs, cfgEmbedder{}, nil) // default dims = 3
	body := `{"files":[{"path":"a.go","content":"x","chunks":[{"start_line":1,"end_line":2,"content":"x","embedding":[0.1,0.2]}]}]}`
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/batch?sync=true", "tok", body)
	if rec.Code != 200 {
		t.Fatalf("batch = %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"errors":1`) || !strings.Contains(rec.Body.String(), `"indexed":0`) {
		t.Errorf("dimension mismatch should count as a per-file error; body %s", rec.Body.String())
	}
}

func TestHandleFilesBatchNoContentNoChunks(t *testing.T) {
	// A file with neither raw content nor pre-computed chunks has nothing to
	// index and is counted as a per-file error.
	writeTok := &store.Token{Scopes: []string{"write"}}
	fs := &fakeStore{token: writeTok, project: &store.Project{ID: 1, Name: "p", Model: "m"}}
	srv := New(fs, cfgEmbedder{}, nil)
	body := `{"files":[{"path":"empty.go"}]}`
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/batch?sync=true", "tok", body)
	if rec.Code != 200 {
		t.Fatalf("batch = %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"errors":1`) || !strings.Contains(rec.Body.String(), `"indexed":0`) {
		t.Errorf("a file with no content or chunks should be a per-file error; body %s", rec.Body.String())
	}
}

func TestHandleFilesBatchPreEmbeddedChunkTooLarge(t *testing.T) {
	// A chunk exceeding maxPreChunkChars is rejected → per-file error.
	writeTok := &store.Token{Scopes: []string{"write"}}
	fs := &fakeStore{token: writeTok, project: &store.Project{ID: 1, Name: "p", Model: "m"}}
	srv := New(fs, cfgEmbedder{}, nil)
	big := strings.Repeat("x", 4001) // > maxPreChunkChars (4000)
	body := `{"files":[{"path":"a.go","content":"x","chunks":[{"start_line":1,"end_line":2,"content":"` + big + `","embedding":[0.1,0.2,0.3]}]}]}`
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/batch?sync=true", "tok", body)
	if rec.Code != 200 {
		t.Fatalf("batch = %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"errors":1`) {
		t.Errorf("oversized chunk should be a per-file error; body %s", rec.Body.String())
	}
}

func TestHandleFilesBatchPreEmbeddedTooManyChunks(t *testing.T) {
	// More than maxPreChunksPerFile chunks is rejected → per-file error.
	writeTok := &store.Token{Scopes: []string{"write"}}
	fs := &fakeStore{token: writeTok, project: &store.Project{ID: 1, Name: "p", Model: "m"}}
	srv := New(fs, cfgEmbedder{}, nil)
	one := `{"start_line":1,"end_line":2,"content":"x","embedding":[0.1,0.2,0.3]}`
	chunks := make([]string, 33) // > maxPreChunksPerFile (32)
	for i := range chunks {
		chunks[i] = one
	}
	body := `{"files":[{"path":"a.go","content":"x","chunks":[` + strings.Join(chunks, ",") + `]}]}`
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/batch?sync=true", "tok", body)
	if rec.Code != 200 {
		t.Fatalf("batch = %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"errors":1`) {
		t.Errorf("too many chunks should be a per-file error; body %s", rec.Body.String())
	}
}

func TestHandleFilesBatchAsync(t *testing.T) {
	writeTok := &store.Token{Scopes: []string{"write"}}
	fs := &fakeStore{
		token:      writeTok,
		project:    &store.Project{ID: 1, Name: "p", Model: "m", SourceType: "push"},
		enqueuedID: 42,
	}
	srv := New(fs, cfgEmbedder{}, nil)

	body := `{"files":[{"path":"a.go","content":"package main"}],"delete":["old.go"]}`
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/batch", "tok", body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("async batch = %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"job_id":42`) || !strings.Contains(rec.Body.String(), `"status":"queued"`) {
		t.Errorf("expected job_id and status; body %s", rec.Body.String())
	}
}

func TestHandleFilesBatchAsyncNoSourceType(t *testing.T) {
	// A non-push project should refuse async batch.
	writeTok := &store.Token{Scopes: []string{"write"}}
	fs := &fakeStore{
		token:   writeTok,
		project: &store.Project{ID: 1, Name: "p", Model: "m"}, // SourceType defaults to ""
	}
	srv := New(fs, cfgEmbedder{}, nil)
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/batch", "tok", `{"files":[]}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("async batch for non-push project = %d, want 400; body %s", rec.Code, rec.Body.String())
	}
}

// --- 500 / error paths across handlers ---

func TestServerErrorPaths(t *testing.T) {
	writeTok := &store.Token{Scopes: []string{"write"}}
	readTok := &store.Token{Scopes: []string{"read"}}
	boom := errors.New("boom")

	t.Run("list projects 500", func(t *testing.T) {
		srv := New(&fakeStore{token: readTok, listErr: boom}, fakeEmbedder{}, nil)
		if rec := do(t, srv, "GET", "/api/v1/projects", "tok", ""); rec.Code != 500 {
			t.Errorf("list err = %d, want 500", rec.Code)
		}
	})

	t.Run("get project 500", func(t *testing.T) {
		srv := New(&fakeStore{token: readTok, getErr: boom}, fakeEmbedder{}, nil)
		if rec := do(t, srv, "GET", "/api/v1/projects/p", "tok", ""); rec.Code != 500 {
			t.Errorf("get err = %d, want 500", rec.Code)
		}
	})

	t.Run("delete project 500", func(t *testing.T) {
		srv := New(&fakeStore{token: writeTok, deleteErr: boom}, fakeEmbedder{}, nil)
		if rec := do(t, srv, "DELETE", "/api/v1/projects/p", "tok", ""); rec.Code != 500 {
			t.Errorf("delete err = %d, want 500", rec.Code)
		}
	})

	t.Run("create project 500", func(t *testing.T) {
		srv := New(&fakeStore{token: writeTok, createErr: boom}, fakeEmbedder{}, nil)
		if rec := do(t, srv, "POST", "/api/v1/projects", "tok", `{"name":"x"}`); rec.Code != 500 {
			t.Errorf("create err = %d, want 500", rec.Code)
		}
	})

	t.Run("files diff 500 on list hashes", func(t *testing.T) {
		srv := New(&fakeStore{token: writeTok, project: &store.Project{ID: 1, Name: "p"}, fileHashErr: boom}, fakeEmbedder{}, nil)
		if rec := do(t, srv, "POST", "/api/v1/projects/p/files/diff", "tok", `{"files":{}}`); rec.Code != 500 {
			t.Errorf("diff err = %d, want 500", rec.Code)
		}
	})

	t.Run("files diff 400 on bad body", func(t *testing.T) {
		srv := New(&fakeStore{token: writeTok, project: &store.Project{ID: 1, Name: "p"}}, fakeEmbedder{}, nil)
		if rec := do(t, srv, "POST", "/api/v1/projects/p/files/diff", "tok", `not json`); rec.Code != 400 {
			t.Errorf("diff bad body = %d, want 400", rec.Code)
		}
	})

	t.Run("files diff 404 unknown project", func(t *testing.T) {
		srv := New(&fakeStore{token: writeTok, project: nil}, fakeEmbedder{}, nil)
		if rec := do(t, srv, "POST", "/api/v1/projects/ghost/files/diff", "tok", `{"files":{}}`); rec.Code != 404 {
			t.Errorf("diff unknown project = %d, want 404", rec.Code)
		}
	})

	t.Run("loadProject 500 on generic error", func(t *testing.T) {
		srv := New(&fakeStore{token: writeTok, getErr: boom}, fakeEmbedder{}, nil)
		if rec := do(t, srv, "POST", "/api/v1/projects/p/files/diff", "tok", `{"files":{}}`); rec.Code != 500 {
			t.Errorf("loadProject err = %d, want 500", rec.Code)
		}
	})

	t.Run("enqueue 500 on load project", func(t *testing.T) {
		srv := New(&fakeStore{token: writeTok, getErr: boom}, fakeEmbedder{}, nil)
		if rec := do(t, srv, "POST", "/api/v1/projects/p/index-jobs", "tok", `{}`); rec.Code != 500 {
			t.Errorf("enqueue load err = %d, want 500", rec.Code)
		}
	})

	t.Run("enqueue 500 on enqueue", func(t *testing.T) {
		srv := New(&fakeStore{token: writeTok, project: &store.Project{ID: 1, Name: "p"}, enqueueErr: boom}, fakeEmbedder{}, nil)
		if rec := do(t, srv, "POST", "/api/v1/projects/p/index-jobs", "tok", `{}`); rec.Code != 500 {
			t.Errorf("enqueue err = %d, want 500", rec.Code)
		}
	})

	t.Run("get job 500", func(t *testing.T) {
		srv := New(&fakeStore{token: readTok, jobErr: boom}, fakeEmbedder{}, nil)
		if rec := do(t, srv, "GET", "/api/v1/jobs/7", "tok", ""); rec.Code != 500 {
			t.Errorf("get job err = %d, want 500", rec.Code)
		}
	})

	t.Run("auth 500 on token lookup", func(t *testing.T) {
		srv := New(&fakeStore{tokenErr: boom}, fakeEmbedder{}, nil)
		if rec := do(t, srv, "GET", "/api/v1/projects", "tok", ""); rec.Code != 500 {
			t.Errorf("auth lookup err = %d, want 500", rec.Code)
		}
	})
}

func TestCreateProjectDefaults(t *testing.T) {
	writeTok := &store.Token{Scopes: []string{"write"}}
	srv := New(&fakeStore{token: writeTok}, fakeEmbedder{}, nil)

	// No model/source → defaults to bge-m3 / push and succeeds.
	rec := do(t, srv, "POST", "/api/v1/projects", "tok", `{"name":"defs"}`)
	if rec.Code != 201 {
		t.Fatalf("defaults create = %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"source_type":"push"`) || !strings.Contains(rec.Body.String(), `"model":"bge-m3"`) {
		t.Errorf("defaults not applied; body %s", rec.Body.String())
	}

	// Invalid source type → 400.
	if rec := do(t, srv, "POST", "/api/v1/projects", "tok", `{"name":"x","source":{"type":"svn"}}`); rec.Code != 400 {
		t.Errorf("bad source type = %d, want 400", rec.Code)
	}
}

func TestResolveScopesOpaqueLookupError(t *testing.T) {
	srv := New(&fakeStore{tokenErr: errors.New("db")}, fakeEmbedder{}, nil)
	if _, _, err := srv.resolveScopes(context.Background(), "anytoken"); err == nil {
		t.Error("resolveScopes should surface the store error")
	}
}

// TestOpaqueAuthLifecycle drives the opaque-token path end to end over HTTP and
// asserts exact status codes for missing/valid/insufficient/revoked tokens.
func TestOpaqueAuthLifecycle(t *testing.T) {
	fs := &fakeStore{
		token:   &store.Token{ID: 1, Name: "cli", Scopes: []string{"read"}},
		project: &store.Project{Name: "p", Model: "m"},
		listed:  []store.Project{{Name: "p"}},
	}
	srv := New(fs, fakeEmbedder{}, nil)

	// No bearer → 401 with the "missing bearer token" message.
	rec := do(t, srv, "GET", "/api/v1/projects", "", "")
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "missing bearer token") {
		t.Errorf("no token: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// Valid read token on a read route → 200.
	if rec := do(t, srv, "GET", "/api/v1/projects", "semidx_live", ""); rec.Code != http.StatusOK {
		t.Errorf("valid read token = %d, want 200", rec.Code)
	}

	// Read token on a write route → 403 naming the required scope.
	rec = do(t, srv, "POST", "/api/v1/projects", "semidx_live", `{"name":"x"}`)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "write") {
		t.Errorf("insufficient scope: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// Revoke: the store no longer resolves the hash → 401 "invalid token".
	fs.token = nil
	rec = do(t, srv, "GET", "/api/v1/projects", "semidx_live", "")
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "invalid token") {
		t.Errorf("revoked token: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestJWTAuthLifecycle drives the JWT control-token path end to end: a valid
// bearer authorizes from its claims, a tampered token and a revoked jti are
// rejected, and an insufficient scope is 403.
func TestJWTAuthLifecycle(t *testing.T) {
	iss, err := jwtauth.New("top-secret")
	if err != nil {
		t.Fatal(err)
	}
	minted, err := iss.Mint("alice", []string{"read"}, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	// jti active in the store → claims scopes apply.
	fs := &fakeStore{token: &store.Token{ID: 1, Kind: "jwt"}, listed: []store.Project{{Name: "p"}}}
	srv := New(fs, fakeEmbedder{}, nil)
	if err := srv.EnableJWT("top-secret"); err != nil {
		t.Fatal(err)
	}

	if rec := do(t, srv, "GET", "/api/v1/projects", minted.Token, ""); rec.Code != http.StatusOK {
		t.Errorf("valid JWT read = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// A write route needs the write/admin scope → 403.
	if rec := do(t, srv, "POST", "/api/v1/projects", minted.Token, `{"name":"x"}`); rec.Code != http.StatusForbidden {
		t.Errorf("JWT lacking write scope = %d, want 403", rec.Code)
	}

	// Tampered JWT: signature no longer matches → falls through to opaque lookup,
	// which the store answers for any hash here, so force that miss separately.
	tampered := minted.Token[:len(minted.Token)-3] + "AAA"
	fsNoOpaque := &fakeStore{token: nil, listed: []store.Project{{Name: "p"}}}
	srv2 := New(fsNoOpaque, fakeEmbedder{}, nil)
	_ = srv2.EnableJWT("top-secret")
	if rec := do(t, srv2, "GET", "/api/v1/projects", tampered, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("tampered JWT = %d, want 401", rec.Code)
	}

	// Revoked jti: store returns nil for the jti → 401.
	fsRevoked := &fakeStore{token: nil}
	srv3 := New(fsRevoked, fakeEmbedder{}, nil)
	_ = srv3.EnableJWT("top-secret")
	if rec := do(t, srv3, "GET", "/api/v1/projects", minted.Token, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked jti = %d, want 401", rec.Code)
	}
}

// --- job workers ---

func newTempProjectDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() { println(\"hi\") }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestClaimAndRun(t *testing.T) {
	t.Run("claims and runs one job", func(t *testing.T) {
		// A push project with no path fails fast (no indexable source) → FailJob.
		fs := &fakeStore{
			claimJob: &store.Job{ID: 1, ProjectID: 9, Type: "full"},
			projByID: &store.Project{ID: 9, Name: "p", SourceType: "push", Path: ""},
		}
		srv := New(fs, fakeEmbedder{}, nil)
		if !srv.claimAndRun(context.Background(), t.TempDir()) {
			t.Error("claimAndRun = false, want true (a job was claimed)")
		}
		if !fs.failCalled || !strings.Contains(fs.failMsg, "no indexable source") {
			t.Errorf("expected fail with no-source message; got called=%v msg=%q", fs.failCalled, fs.failMsg)
		}
	})

	t.Run("no queued job", func(t *testing.T) {
		fs := &fakeStore{}
		if New(fs, fakeEmbedder{}, nil).claimAndRun(context.Background(), t.TempDir()) {
			t.Error("claimAndRun = true, want false (queue empty)")
		}
	})

	t.Run("claim error", func(t *testing.T) {
		fs := &fakeStore{claimErr: errors.New("db")}
		if New(fs, fakeEmbedder{}, nil).claimAndRun(context.Background(), t.TempDir()) {
			t.Error("claimAndRun = true on claim error, want false")
		}
	})
}

func TestRunJob(t *testing.T) {
	ctx := context.Background()

	t.Run("project lookup fails", func(t *testing.T) {
		fs := &fakeStore{projByIDErr: errors.New("gone")}
		srv := New(fs, fakeEmbedder{}, nil)
		srv.runJob(ctx, &store.Job{ID: 1, ProjectID: 2}, t.TempDir())
		if !fs.failCalled || !strings.Contains(fs.failMsg, "project not found") {
			t.Errorf("expected project-not-found fail; msg=%q", fs.failMsg)
		}
	})

	t.Run("model info error", func(t *testing.T) {
		fs := &fakeStore{projByID: &store.Project{ID: 2, Name: "p", SourceType: "path", Path: newTempProjectDir(t), Model: "m"}}
		srv := New(fs, cfgEmbedder{modelInfoErr: errors.New("no model")}, nil)
		srv.runJob(ctx, &store.Job{ID: 1, ProjectID: 2, Type: "full"}, t.TempDir())
		if !fs.failCalled || !strings.Contains(fs.failMsg, "model info") {
			t.Errorf("expected model-info fail; msg=%q", fs.failMsg)
		}
	})

	t.Run("ensure chunks table error", func(t *testing.T) {
		fs := &fakeStore{
			projByID:  &store.Project{ID: 2, Name: "p", SourceType: "path", Path: newTempProjectDir(t), Model: "m"},
			ensureErr: errors.New("no table"),
		}
		srv := New(fs, cfgEmbedder{}, nil)
		srv.runJob(ctx, &store.Job{ID: 1, ProjectID: 2, Type: "full"}, t.TempDir())
		if !fs.failCalled || !strings.Contains(fs.failMsg, "ensure chunks table") {
			t.Errorf("expected ensure-table fail; msg=%q", fs.failMsg)
		}
	})

	t.Run("successful full index completes the job", func(t *testing.T) {
		fs := &fakeStore{projByID: &store.Project{ID: 2, Name: "p", SourceType: "path", Path: newTempProjectDir(t), Model: "m"}}
		srv := New(fs, cfgEmbedder{}, nil)
		srv.runJob(ctx, &store.Job{ID: 1, ProjectID: 2, Type: "full"}, t.TempDir())
		if fs.failCalled {
			t.Errorf("index should not have failed: %q", fs.failMsg)
		}
		if !fs.compCalled || fs.compFiles < 1 {
			t.Errorf("expected CompleteJob with >=1 file; called=%v files=%d", fs.compCalled, fs.compFiles)
		}
	})
}

func TestWorkerStopsOnContextCancel(t *testing.T) {
	fs := &fakeStore{}
	srv := New(fs, fakeEmbedder{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled → worker returns on the first select
	done := make(chan struct{})
	go func() {
		srv.worker(ctx, t.TempDir())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not return on cancelled context")
	}
}

func TestStartWorkersDrainsQueuedJob(t *testing.T) {
	// n<1 is normalised to 1. The single worker's ticker fires (~2s), drains the
	// one queued job (a push project with no path → FailJob), and we observe the
	// failure message over a channel (race-free), then cancel.
	failCh := make(chan string, 1)
	fs := &fakeStore{
		failCh:   failCh,
		claimJob: &store.Job{ID: 1, ProjectID: 9, Type: "full"},
		projByID: &store.Project{ID: 9, Name: "p", SourceType: "push", Path: ""},
	}
	srv := New(fs, fakeEmbedder{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.StartWorkers(ctx, 0, t.TempDir())
	select {
	case msg := <-failCh:
		if !strings.Contains(msg, "no indexable source") {
			t.Errorf("unexpected fail message: %q", msg)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("worker did not drain the queued job")
	}
}
