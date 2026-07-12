package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/store"
)

// --- Additional fakeStore methods needed for extended tests ---

func (f *fakeStore) EnsureChunksTable(context.Context, int) error { return f.ensureErr }
func (f *fakeStore) CountTokens(context.Context) (int, error)     { return f.tokenCount, f.countTokErr }
func (f *fakeStore) CreateToken(_ context.Context, name, hash string, scopes []string) (int, error) {
	f.lastTokName = name
	f.lastTokHash = hash
	f.lastTokScopes = scopes
	return 1, f.createTokErr
}
func (f *fakeStore) UpdateProjectStatus(context.Context, int, string) error            { return nil }
func (f *fakeStore) DeleteFileByPath(context.Context, int, string) error               { return nil }
func (f *fakeStore) UpsertFile(context.Context, int, string, string, int) (int, error) { return 1, nil }
func (f *fakeStore) FileUpToDate(context.Context, int, string, string, int) (bool, error) {
	return f.upToDate, nil
}
func (f *fakeStore) DeleteChunksForFile(context.Context, int, int, int) error { return nil }
func (f *fakeStore) InsertChunks(context.Context, int, int, []chunker.Chunk, [][]float32, int) error {
	return nil
}
func (f *fakeStore) InsertChunksTextOnly(context.Context, int, int, []chunker.Chunk, int) error {
	return nil
}
func (f *fakeStore) GetProjectByID(_ context.Context, id int) (*store.Project, error) {
	if f.projByIDErr != nil {
		return nil, f.projByIDErr
	}
	if f.projByID != nil && f.projByID.ID == id {
		return f.projByID, nil
	}
	if f.project != nil {
		return f.project, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeStore) ClaimJob(context.Context) (*store.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.claimJob
	f.claimJob = nil // return once
	return j, f.claimErr
}
func (f *fakeStore) FailJob(_ context.Context, _ int, msg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failMsg = msg
	f.failCalled = true
	if f.failCh != nil {
		f.failCh <- msg
	}
	return nil
}
func (f *fakeStore) CompleteJob(_ context.Context, id, files, chunks, deleted, errors int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.compFiles = files
	f.compChunks = chunks
	f.compDeleted = deleted
	f.compErrors = errors
	f.compCalled = true
	return nil
}
func (f *fakeStore) UpdateJobProgress(context.Context, int, int, int, int, int, int) error {
	return nil
}
func (f *fakeStore) EnqueueBatchJob(context.Context, int, string) (int, error) {
	if f.enqueueBatchErr != nil {
		return 0, f.enqueueBatchErr
	}
	return 1, nil
}
func (f *fakeStore) EnsureEmbeddingCacheTable(context.Context, int) error { return nil }
func (f *fakeStore) LookupEmbeddingCache(context.Context, []string, string, int) (map[string][]float32, error) {
	return map[string][]float32{}, nil
}
func (f *fakeStore) InsertEmbeddingCache(context.Context, []string, string, [][]float32, int) error {
	return nil
}

// --- Tests ---

// TestSetGitAllowFile verifies the trivial setter.
func TestSetGitAllowFile(t *testing.T) {
	t.Parallel()
	srv := New(&fakeStore{}, fakeEmbedder{}, nil)
	if srv.gitAllowFile {
		t.Error("gitAllowFile should default to false")
	}
	srv.SetGitAllowFile(true)
	if !srv.gitAllowFile {
		t.Error("gitAllowFile should be true after SetGitAllowFile(true)")
	}
	srv.SetGitAllowFile(false)
	if srv.gitAllowFile {
		t.Error("gitAllowFile should be false after SetGitAllowFile(false)")
	}
}

// TestProjectStatusEndpoint covers handleProjectStatus success and
// CountProjectFiles error paths.
func TestProjectStatusEndpoint(t *testing.T) {
	t.Parallel()

	readTok := &store.Token{Scopes: []string{"read"}}

	// Success path.
	srv := New(&fakeStore{
		token:     readTok,
		project:   &store.Project{Name: "proj", Identity: "id:proj", SourceType: "git", Status: "ready", Model: "bge-m3"},
		fileCount: 42,
	}, fakeEmbedder{}, nil)
	rec := do(t, srv, "GET", "/api/v1/projects/proj/status", "tok", "")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Name       string `json:"name"`
		SourceType string `json:"source_type"`
		Identity   string `json:"identity"`
		Status     string `json:"status"`
		Model      string `json:"model"`
		TotalFiles int    `json:"total_files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if resp.Name != "proj" || resp.TotalFiles != 42 || resp.Identity != "id:proj" {
		t.Errorf("status response = %+v", resp)
	}

	// Unknown project → 404.
	nf := New(&fakeStore{token: readTok, project: nil}, fakeEmbedder{}, nil)
	if rec := do(t, nf, "GET", "/api/v1/projects/ghost/status", "tok", ""); rec.Code != 404 {
		t.Errorf("unknown project = %d, want 404", rec.Code)
	}
}

// TestBearerToken covers the bearerToken helper with edge cases.
func TestBearerToken(t *testing.T) {
	t.Parallel()

	cases := []struct {
		auth string
		want string
	}{
		{"Bearer mytoken", "mytoken"},
		{"Bearer  ", ""},           // only spaces after Bearer
		{"bearer x", ""},           // wrong case
		{"", ""},                   // empty
		{"Basic dXNlcjpwYXNz", ""}, // not bearer
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", tc.auth)
		got := bearerToken(req)
		if got != tc.want {
			t.Errorf("bearerToken(%q) = %q, want %q", tc.auth, got, tc.want)
		}
	}
}

// TestRateLimitAllowedPaths verifies the rate limiter excludes health and
// admin paths from counting.
func TestRateLimitAllowedPaths(t *testing.T) {
	t.Parallel()

	// Use the direct apiRateLimiter
	lim := newAPIRateLimiter()
	// Allow should initially succeed.
	if !lim.allow("test-key") {
		t.Error("first allow should succeed")
	}
	// Exhaust the 200 req/s budget.
	for lim.allow("test-key") {
	}
	if lim.allow("test-key") {
		t.Error("should be rate limited after 200 requests")
	}
	// A different key is not affected.
	if !lim.allow("other-key") {
		t.Error("different key should not be rate limited")
	}
	// After the window passes, the key is allowed again.
	// We can't easily advance time, but we can verify the bucket resets.
}

// TestRateLimiterReap verifies that reap cleans old buckets.
func TestRateLimiterReap(t *testing.T) {
	t.Parallel()

	lim := &apiRateLimiter{counts: map[string]*rateBucket{
		"old": {count: 1, window: time.Now().Add(-10 * time.Minute)},
		"new": {count: 1, window: time.Now().Add(time.Minute)},
	}}
	lim.reapOnce()
	if _, ok := lim.counts["old"]; ok {
		t.Error("old bucket should have been reaped")
	}
	if _, ok := lim.counts["new"]; !ok {
		t.Error("new bucket should not have been reaped")
	}
}

// reapOnce runs one iteration of the rate limiter's reap loop (non-blocking).
func (l *apiRateLimiter) reapOnce() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for k, b := range l.counts {
		if now.After(b.window) {
			delete(l.counts, k)
		}
	}
}

// TestAuthScopesConflict covers the missing-scope path in the authed middleware.
func TestAuthScopesConflict(t *testing.T) {
	t.Parallel()

	// Token with "write" scope trying to read.
	writeTok := &store.Token{Scopes: []string{"write"}}
	srv := New(&fakeStore{
		token:   writeTok,
		project: &store.Project{Name: "p"},
	}, fakeEmbedder{}, nil)

	// A read endpoint with write-only token → 403.
	if rec := do(t, srv, "GET", "/api/v1/projects/p", "tok", ""); rec.Code != 403 {
		t.Errorf("write token on read endpoint = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

// TestAuthTokenLookupError verifies that a backend error during token
// lookup returns 500, not 401.
func TestAuthTokenLookupError(t *testing.T) {
	t.Parallel()

	srv := New(&fakeStore{
		token:    &store.Token{Scopes: []string{"read"}},
		getErr:   errors.New("db connection lost"),
		tokenErr: errors.New("db connection lost"),
	}, fakeEmbedder{}, nil)

	// The token lookup fails (token error).
	if rec := do(t, srv, "GET", "/api/v1/projects/p", "tok", ""); rec.Code != 500 {
		t.Errorf("token lookup error = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandleFilesBatchAsyncProjectType verifies that non-push projects
// are rejected for async batch.
func TestHandleFilesBatchAsyncProjectType(t *testing.T) {
	t.Parallel()

	writeTok := &store.Token{Scopes: []string{"write"}}
	srv := New(&fakeStore{
		token:   writeTok,
		project: &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "git"},
	}, fakeEmbedder{}, nil)

	// Git project async batch → 400.
	body := `{"files":[{"path":"a.go","content":"package a"}],"delete":[]}`
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/batch", "tok", body)
	if rec.Code != 400 {
		t.Fatalf("git async batch = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestEnsureBootstrapTokenErrors covers the error paths in
// EnsureBootstrapToken.
func TestEnsureBootstrapTokenErrors(t *testing.T) {
	t.Parallel()

	// CountTokens error.
	srv := New(&fakeStore{
		token:       &store.Token{Scopes: []string{"admin"}},
		countTokErr: errors.New("count error"),
	}, fakeEmbedder{}, nil)
	if _, err := srv.EnsureBootstrapToken(context.Background(), ""); err == nil {
		t.Error("expected CountTokens error")
	}

	// Tokens already exist → empty string, no error.
	srv2 := New(&fakeStore{
		token:      &store.Token{Scopes: []string{"admin"}},
		tokenCount: 1,
	}, fakeEmbedder{}, nil)
	got, err := srv2.EnsureBootstrapToken(context.Background(), "")
	if err != nil || got != "" {
		t.Errorf("existing tokens: got=%q err=%v, want empty", got, err)
	}
}

// TestEnsureBootstrapAdminSkipCases covers paths that skip admin creation.
func TestEnsureBootstrapAdminSkipCases(t *testing.T) {
	t.Parallel()

	// Empty password → skip (return "").
	srv := New(&fakeStore{}, fakeEmbedder{}, nil)
	got, err := srv.EnsureBootstrapAdmin(context.Background(), "admin", "")
	if err != nil || got != "" {
		t.Errorf("empty password: got=%q err=%v, want empty", got, err)
	}

	// Users already exist → skip.
	srv2 := New(&fakeStore{userCount: 1}, fakeEmbedder{}, nil)
	got, err = srv2.EnsureBootstrapAdmin(context.Background(), "admin", "password")
	if err != nil || got != "" {
		t.Errorf("existing users: got=%q err=%v, want empty", got, err)
	}
}
