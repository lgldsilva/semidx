package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/store"
)

// fakeStore implements just the methods the server touches.
type fakeStore struct {
	store.Store
	pingErr    error
	token      *store.Token         // TokenByHash result (nil = no active token)
	project    *store.Project       // GetProject result (nil → ErrNotFound)
	results    []store.SearchResult // search results
	createErr  error                // CreateProject error
	listed     []store.Project      // ListProjects result
	deleteErr  error                // DeleteProject error
	enqueuedID int                  // EnqueueJob result
	job        *store.Job           // GetJob result (nil → ErrNotFound)
	fileHashes map[string]string    // ListFileHashes result
	userCount  int                  // CountUsers result
	created    *store.User          // last CreateUser call

	// error-injection fields (all nil/zero = success path)
	listErr     error // ListProjects error
	getErr      error // GetProject generic (non-NotFound) error
	tokenErr    error // TokenByHash error
	fileHashErr error // ListFileHashes error
	enqueueErr  error // EnqueueJob error
	jobErr      error // GetJob generic (non-NotFound) error
	ensureErr   error // EnsureChunksTable error

	// bootstrap-token fields
	tokenCount    int    // CountTokens result
	countTokErr   error  // CountTokens error
	createTokErr  error  // CreateToken error
	lastTokName   string // last CreateToken name
	lastTokHash   string // last CreateToken hash
	lastTokScopes []string

	// job-worker fields
	claimJob    *store.Job // ClaimJob result (returned once, then nil)
	claimErr    error      // ClaimJob error
	projByID    *store.Project
	projByIDErr error
	failMsg     string // last FailJob message
	failCalled  bool
	failCh      chan string // if set, FailJob sends its message (for async worker tests)
	compFiles   int         // last CompleteJob filesIndexed
	compChunks  int         // last CompleteJob chunksCreated
	compCalled  bool
}

func (f *fakeStore) CountUsers(context.Context) (int, error) { return f.userCount, nil }
func (f *fakeStore) CreateUser(_ context.Context, username, hash, role string) (*store.User, error) {
	f.created = &store.User{Username: username, PasswordHash: hash, Role: role}
	return f.created, nil
}

func (f *fakeStore) ListFileHashes(context.Context, int) (map[string]string, error) {
	return f.fileHashes, f.fileHashErr
}
func (f *fakeStore) Ping(context.Context) error { return f.pingErr }
func (f *fakeStore) TokenByHash(context.Context, string) (*store.Token, error) {
	return f.token, f.tokenErr
}
func (f *fakeStore) GetProject(_ context.Context, name string) (*store.Project, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.project == nil {
		return nil, store.ErrNotFound
	}
	return f.project, nil
}
func (f *fakeStore) CreateProject(_ context.Context, name, model, sourceType, gitURL, branch string) (*store.Project, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &store.Project{Name: name, Model: model, Status: "registered", SourceType: sourceType, GitURL: gitURL, Branch: branch}, nil
}
func (f *fakeStore) ListProjects(context.Context, int, int) ([]store.Project, error) {
	return f.listed, f.listErr
}
func (f *fakeStore) DeleteProject(context.Context, string) error { return f.deleteErr }
func (f *fakeStore) EnqueueJob(context.Context, int, string) (int, error) {
	return f.enqueuedID, f.enqueueErr
}
func (f *fakeStore) GetJob(_ context.Context, id int) (*store.Job, error) {
	if f.jobErr != nil {
		return nil, f.jobErr
	}
	if f.job == nil {
		return nil, store.ErrNotFound
	}
	return f.job, nil
}
func (f *fakeStore) SearchSimilar(context.Context, int, []float32, int, int) ([]store.SearchResult, error) {
	return f.results, nil
}
func (f *fakeStore) SearchSimilarKeywords(context.Context, int, string, int, int) ([]store.SearchResult, error) {
	return f.results, nil
}

type fakeEmbedder struct{ embed.Embedder }

func (fakeEmbedder) ModelInfo(_ context.Context, m string) (*embed.ModelInfo, error) {
	return &embed.ModelInfo{Name: m, Dims: 3}, nil
}
func (fakeEmbedder) EmbedSingle(context.Context, string, string) ([]float32, error) {
	return []float32{1, 0, 0}, nil
}

func do(t *testing.T, srv *Server, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestHealthz(t *testing.T) {
	srv := New(&fakeStore{}, fakeEmbedder{}, nil)
	rec := do(t, srv, "GET", "/healthz", "", "")
	if rec.Code != 200 || rec.Body.String() != "ok" {
		t.Errorf("healthz = %d %q", rec.Code, rec.Body.String())
	}
}

func TestReadyz(t *testing.T) {
	ok := New(&fakeStore{}, fakeEmbedder{}, nil)
	if rec := do(t, ok, "GET", "/readyz", "", ""); rec.Code != 200 {
		t.Errorf("readyz (db up) = %d, want 200", rec.Code)
	}
	down := New(&fakeStore{pingErr: errors.New("down")}, fakeEmbedder{}, nil)
	if rec := do(t, down, "GET", "/readyz", "", ""); rec.Code != 503 {
		t.Errorf("readyz (db down) = %d, want 503", rec.Code)
	}
}

func TestMetrics(t *testing.T) {
	srv := New(&fakeStore{}, fakeEmbedder{}, nil)
	_ = do(t, srv, "GET", "/healthz", "", "") // generate a metric
	rec := do(t, srv, "GET", "/metrics", "", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "semidx_http_requests_total") {
		t.Errorf("metrics missing counter; code=%d", rec.Code)
	}
}

func TestSearchAuth(t *testing.T) {
	readToken := &store.Token{ID: 1, Name: "t", Scopes: []string{"read"}}
	body := `{"query":"hi"}`

	// No token → 401.
	if rec := do(t, New(&fakeStore{token: readToken, project: &store.Project{Name: "p", Model: "m"}}, fakeEmbedder{}, nil),
		"POST", "/api/v1/projects/p/search", "", body); rec.Code != 401 {
		t.Errorf("no token = %d, want 401", rec.Code)
	}
	// Invalid token (store returns nil) → 401.
	if rec := do(t, New(&fakeStore{token: nil}, fakeEmbedder{}, nil),
		"POST", "/api/v1/projects/p/search", "bad", body); rec.Code != 401 {
		t.Errorf("invalid token = %d, want 401", rec.Code)
	}
	// Token without the read scope → 403.
	writeOnly := &store.Token{ID: 2, Scopes: []string{"write"}}
	if rec := do(t, New(&fakeStore{token: writeOnly, project: &store.Project{Name: "p", Model: "m"}}, fakeEmbedder{}, nil),
		"POST", "/api/v1/projects/p/search", "tok", body); rec.Code != 403 {
		t.Errorf("missing scope = %d, want 403", rec.Code)
	}
}

func TestSearchOK(t *testing.T) {
	srv := New(&fakeStore{
		token:   &store.Token{Scopes: []string{"read"}},
		project: &store.Project{ID: 1, Name: "proj", Model: "bge-m3"},
		results: []store.SearchResult{{FilePath: "a.go", StartLine: 5, EndLine: 7, Score: 0.9, Content: "x"}},
	}, fakeEmbedder{}, nil)

	rec := do(t, srv, "POST", "/api/v1/projects/proj/search", "tok", `{"query":"auth","top_k":3}`)
	if rec.Code != 200 {
		t.Fatalf("search = %d, body %s", rec.Code, rec.Body.String())
	}
	var out searchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if out.Project != "proj" || out.Model != "bge-m3" || len(out.Results) != 1 {
		t.Errorf("unexpected response: %+v", out)
	}
	if out.Results[0].Path != "a.go" || out.Results[0].StartLine != 5 {
		t.Errorf("hit = %+v", out.Results[0])
	}
}

func TestSearchProjectNotFound(t *testing.T) {
	srv := New(&fakeStore{token: &store.Token{Scopes: []string{"read"}}, project: nil}, fakeEmbedder{}, nil)
	rec := do(t, srv, "POST", "/api/v1/projects/ghost/search", "tok", `{"query":"hi"}`)
	if rec.Code != 404 {
		t.Errorf("missing project = %d, want 404", rec.Code)
	}
}

func TestSearchBadBody(t *testing.T) {
	srv := New(&fakeStore{token: &store.Token{Scopes: []string{"read"}}, project: &store.Project{Name: "p"}}, fakeEmbedder{}, nil)
	if rec := do(t, srv, "POST", "/api/v1/projects/p/search", "tok", `not json`); rec.Code != 400 {
		t.Errorf("bad body = %d, want 400", rec.Code)
	}
	if rec := do(t, srv, "POST", "/api/v1/projects/p/search", "tok", `{"query":""}`); rec.Code != 400 {
		t.Errorf("empty query = %d, want 400", rec.Code)
	}
}

func TestCreateProject(t *testing.T) {
	writeTok := &store.Token{Scopes: []string{"write"}}

	// git project, valid → 201.
	srv := New(&fakeStore{token: writeTok}, fakeEmbedder{}, nil)
	rec := do(t, srv, "POST", "/api/v1/projects", "tok", `{"name":"repo","model":"bge-m3","source":{"type":"git","url":"https://x/y.git"}}`)
	if rec.Code != 201 {
		t.Fatalf("create = %d, body %s", rec.Code, rec.Body.String())
	}
	var pv projectView
	_ = json.Unmarshal(rec.Body.Bytes(), &pv)
	if pv.Name != "repo" || pv.SourceType != "git" || pv.GitURL != "https://x/y.git" {
		t.Errorf("view = %+v", pv)
	}

	// duplicate → 409.
	dup := New(&fakeStore{token: writeTok, createErr: store.ErrProjectExists}, fakeEmbedder{}, nil)
	if rec := do(t, dup, "POST", "/api/v1/projects", "tok", `{"name":"repo"}`); rec.Code != 409 {
		t.Errorf("duplicate = %d, want 409", rec.Code)
	}
	// missing name → 400.
	if rec := do(t, srv, "POST", "/api/v1/projects", "tok", `{"model":"m"}`); rec.Code != 400 {
		t.Errorf("no name = %d, want 400", rec.Code)
	}
	// git without url → 400.
	if rec := do(t, srv, "POST", "/api/v1/projects", "tok", `{"name":"g","source":{"type":"git"}}`); rec.Code != 400 {
		t.Errorf("git no url = %d, want 400", rec.Code)
	}
	// read-only token → 403.
	ro := New(&fakeStore{token: &store.Token{Scopes: []string{"read"}}}, fakeEmbedder{}, nil)
	if rec := do(t, ro, "POST", "/api/v1/projects", "tok", `{"name":"x"}`); rec.Code != 403 {
		t.Errorf("read-only create = %d, want 403", rec.Code)
	}
}

func TestListAndGetProject(t *testing.T) {
	readTok := &store.Token{Scopes: []string{"read"}}
	srv := New(&fakeStore{
		token:   readTok,
		listed:  []store.Project{{Name: "a", Model: "m", Status: "ready", SourceType: "push"}},
		project: &store.Project{Name: "a", Model: "m", Status: "ready", SourceType: "push"},
	}, fakeEmbedder{}, nil)

	rec := do(t, srv, "GET", "/api/v1/projects", "tok", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"name":"a"`) {
		t.Errorf("list = %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, srv, "GET", "/api/v1/projects/a", "tok", ""); rec.Code != 200 {
		t.Errorf("get = %d, want 200", rec.Code)
	}
	// unknown project → 404.
	nf := New(&fakeStore{token: readTok, project: nil}, fakeEmbedder{}, nil)
	if rec := do(t, nf, "GET", "/api/v1/projects/ghost", "tok", ""); rec.Code != 404 {
		t.Errorf("get missing = %d, want 404", rec.Code)
	}
}

func TestDeleteProject(t *testing.T) {
	writeTok := &store.Token{Scopes: []string{"write"}}
	srv := New(&fakeStore{token: writeTok}, fakeEmbedder{}, nil)
	if rec := do(t, srv, "DELETE", "/api/v1/projects/a", "tok", ""); rec.Code != 204 {
		t.Errorf("delete = %d, want 204", rec.Code)
	}
	nf := New(&fakeStore{token: writeTok, deleteErr: store.ErrNotFound}, fakeEmbedder{}, nil)
	if rec := do(t, nf, "DELETE", "/api/v1/projects/ghost", "tok", ""); rec.Code != 404 {
		t.Errorf("delete missing = %d, want 404", rec.Code)
	}
	// read-only token → 403.
	ro := New(&fakeStore{token: &store.Token{Scopes: []string{"read"}}}, fakeEmbedder{}, nil)
	if rec := do(t, ro, "DELETE", "/api/v1/projects/a", "tok", ""); rec.Code != 403 {
		t.Errorf("read-only delete = %d, want 403", rec.Code)
	}
}

func TestEnqueueJob(t *testing.T) {
	writeTok := &store.Token{Scopes: []string{"write"}}
	srv := New(&fakeStore{token: writeTok, project: &store.Project{ID: 1, Name: "p", SourceType: "git"}, enqueuedID: 77}, fakeEmbedder{}, nil)

	rec := do(t, srv, "POST", "/api/v1/projects/p/index-jobs", "tok", `{"type":"full"}`)
	if rec.Code != 202 || !strings.Contains(rec.Body.String(), `"job_id":77`) {
		t.Errorf("enqueue = %d %s", rec.Code, rec.Body.String())
	}
	// empty body defaults to full → 202.
	if rec := do(t, srv, "POST", "/api/v1/projects/p/index-jobs", "tok", ``); rec.Code != 202 {
		t.Errorf("empty-body enqueue = %d, want 202", rec.Code)
	}
	// bad type → 400.
	if rec := do(t, srv, "POST", "/api/v1/projects/p/index-jobs", "tok", `{"type":"bogus"}`); rec.Code != 400 {
		t.Errorf("bad type = %d, want 400", rec.Code)
	}
	// unknown project → 404.
	nf := New(&fakeStore{token: writeTok, project: nil}, fakeEmbedder{}, nil)
	if rec := do(t, nf, "POST", "/api/v1/projects/ghost/index-jobs", "tok", `{}`); rec.Code != 404 {
		t.Errorf("unknown project = %d, want 404", rec.Code)
	}
	// read-only token → 403.
	ro := New(&fakeStore{token: &store.Token{Scopes: []string{"read"}}}, fakeEmbedder{}, nil)
	if rec := do(t, ro, "POST", "/api/v1/projects/p/index-jobs", "tok", `{}`); rec.Code != 403 {
		t.Errorf("read-only enqueue = %d, want 403", rec.Code)
	}
}

func TestGetJob(t *testing.T) {
	readTok := &store.Token{Scopes: []string{"read"}}
	srv := New(&fakeStore{token: readTok, job: &store.Job{ID: 5, Type: "full", Status: "succeeded", FilesIndexed: 3, ChunksCreated: 9}}, fakeEmbedder{}, nil)

	rec := do(t, srv, "GET", "/api/v1/jobs/5", "tok", "")
	if rec.Code != 200 {
		t.Fatalf("get job = %d, body %s", rec.Code, rec.Body.String())
	}
	var jv jobView
	_ = json.Unmarshal(rec.Body.Bytes(), &jv)
	if jv.ID != 5 || jv.Status != "succeeded" || jv.FilesIndexed != 3 {
		t.Errorf("job view = %+v", jv)
	}
	// non-integer id → 400.
	if rec := do(t, srv, "GET", "/api/v1/jobs/abc", "tok", ""); rec.Code != 400 {
		t.Errorf("bad id = %d, want 400", rec.Code)
	}
	// unknown job → 404.
	nf := New(&fakeStore{token: readTok, job: nil}, fakeEmbedder{}, nil)
	if rec := do(t, nf, "GET", "/api/v1/jobs/9999", "tok", ""); rec.Code != 404 {
		t.Errorf("unknown job = %d, want 404", rec.Code)
	}
}

func TestFilesDiff(t *testing.T) {
	writeTok := &store.Token{Scopes: []string{"write"}}
	srv := New(&fakeStore{
		token:      writeTok,
		project:    &store.Project{ID: 1, Name: "p"},
		fileHashes: map[string]string{"a.go": "h1", "old.go": "h9"}, // indexed already
	}, fakeEmbedder{}, nil)

	// Client has a.go (unchanged), b.go (new), and dropped old.go.
	rec := do(t, srv, "POST", "/api/v1/projects/p/files/diff", "tok",
		`{"files":{"a.go":"h1","b.go":"h2"}}`)
	if rec.Code != 200 {
		t.Fatalf("diff = %d, body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Stale   []string `json:"stale"`
		Deleted []string `json:"deleted"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Stale) != 1 || out.Stale[0] != "b.go" {
		t.Errorf("stale = %v, want [b.go]", out.Stale)
	}
	if len(out.Deleted) != 1 || out.Deleted[0] != "old.go" {
		t.Errorf("deleted = %v, want [old.go]", out.Deleted)
	}
}

func TestFilesBatchValidation(t *testing.T) {
	writeTok := &store.Token{Scopes: []string{"write"}}
	// unknown project → 404 (before touching the embedder).
	nf := New(&fakeStore{token: writeTok, project: nil}, fakeEmbedder{}, nil)
	if rec := do(t, nf, "POST", "/api/v1/projects/ghost/files/batch", "tok", `{"files":[]}`); rec.Code != 404 {
		t.Errorf("unknown project = %d, want 404", rec.Code)
	}
	// bad body → 400.
	ok := New(&fakeStore{token: writeTok, project: &store.Project{ID: 1, Name: "p", Model: "m"}}, fakeEmbedder{}, nil)
	if rec := do(t, ok, "POST", "/api/v1/projects/p/files/batch", "tok", `not json`); rec.Code != 400 {
		t.Errorf("bad body = %d, want 400", rec.Code)
	}
	// read-only token → 403.
	ro := New(&fakeStore{token: &store.Token{Scopes: []string{"read"}}}, fakeEmbedder{}, nil)
	if rec := do(t, ro, "POST", "/api/v1/projects/p/files/diff", "tok", `{"files":{}}`); rec.Code != 403 {
		t.Errorf("read-only diff = %d, want 403", rec.Code)
	}
}

func TestTokenGenerationAndHash(t *testing.T) {
	p1, h1, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	p2, _, _ := GenerateToken()
	if p1 == p2 {
		t.Error("generated tokens should differ")
	}
	if !strings.HasPrefix(p1, "semidx_") {
		t.Errorf("token %q missing prefix", p1)
	}
	if h1 != HashToken(p1) || h1 == p1 {
		t.Error("HashToken must be deterministic and not the plaintext")
	}
}
