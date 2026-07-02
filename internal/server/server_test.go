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
	pingErr error
	token   *store.Token         // TokenByHash result (nil = no active token)
	project *store.Project       // GetProject result (nil → not found)
	results []store.SearchResult // search results
}

func (f *fakeStore) Ping(context.Context) error { return f.pingErr }
func (f *fakeStore) TokenByHash(context.Context, string) (*store.Token, error) {
	return f.token, nil
}
func (f *fakeStore) GetProject(_ context.Context, name string) (*store.Project, error) {
	if f.project == nil {
		return nil, errors.New("no rows")
	}
	return f.project, nil
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
