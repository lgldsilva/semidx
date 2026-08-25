package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

// runtimeErrStore exercises the failure branches of the runtime-graph and
// privacy endpoints: every optional-store call can be made to fail.
type runtimeErrStore struct {
	*fakeStore
	listErrR   error
	listWSErrR error
	upsertErr  error
	privacyErr error
	privacy    string
}

func (s *runtimeErrStore) UpsertRuntimeEdges(context.Context, int, []store.RuntimeEdge) error {
	return s.upsertErr
}

func (s *runtimeErrStore) ListRuntimeEdges(context.Context, int) ([]store.RuntimeEdge, error) {
	return nil, s.listErrR
}

func (s *runtimeErrStore) ListWorkspaceRuntimeEdges(context.Context, int) ([]store.RuntimeEdge, error) {
	return nil, s.listWSErrR
}

func (s *runtimeErrStore) SetProjectPrivacy(_ context.Context, _ int, mode string) error {
	if s.privacyErr != nil {
		return s.privacyErr
	}
	s.privacy = mode
	return nil
}

func newRuntimeErrStore(project *store.Project, scopes ...string) *runtimeErrStore {
	return &runtimeErrStore{fakeStore: &fakeStore{
		token:   &store.Token{Scopes: scopes},
		project: project,
	}}
}

func TestListRuntimeEdgesErrors(t *testing.T) {
	proj := &store.Project{ID: 3, Name: "repo"}

	plain := &fakeStore{token: &store.Token{Scopes: []string{"read"}}, project: proj}
	if rec := do(t, New(plain, fakeEmbedder{}, nil), "GET", "/api/v1/projects/repo/runtime-edges", "tok", ""); rec.Code != http.StatusNotImplemented {
		t.Errorf("store without runtime graph = %d, want 501", rec.Code)
	}

	missing := newRuntimeErrStore(nil, "read")
	if rec := do(t, New(missing, fakeEmbedder{}, nil), "GET", "/api/v1/projects/ghost/runtime-edges", "tok", ""); rec.Code != http.StatusNotFound {
		t.Errorf("missing project = %d, want 404", rec.Code)
	}

	broken := newRuntimeErrStore(proj, "read")
	broken.getErr = errors.New("db down")
	if rec := do(t, New(broken, fakeEmbedder{}, nil), "GET", "/api/v1/projects/repo/runtime-edges", "tok", ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("project lookup failure = %d, want 500", rec.Code)
	}

	listBroken := newRuntimeErrStore(proj, "read")
	listBroken.listErrR = errors.New("boom")
	if rec := do(t, New(listBroken, fakeEmbedder{}, nil), "GET", "/api/v1/projects/repo/runtime-edges", "tok", ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("list failure = %d, want 500", rec.Code)
	}
}

func TestListWorkspaceRuntimeEdgesLimits(t *testing.T) {
	st := newRuntimeErrStore(&store.Project{ID: 3, Name: "repo"}, "read")
	srv := New(st, fakeEmbedder{}, nil)

	// A limit above the cap is clamped rather than rejected.
	rec := do(t, srv, "GET", "/api/v1/runtime-graph?limit=100000", "tok", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("clamped limit = %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"limit":5000`) {
		t.Errorf("body = %s, want the limit clamped to 5000", rec.Body.String())
	}

	for _, q := range []string{"limit=0", "limit=-3", "limit=abc"} {
		if rec := do(t, srv, "GET", "/api/v1/runtime-graph?"+q, "tok", ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", q, rec.Code)
		}
	}

	broken := newRuntimeErrStore(nil, "read")
	broken.listWSErrR = errors.New("db down")
	if rec := do(t, New(broken, fakeEmbedder{}, nil), "GET", "/api/v1/runtime-graph", "tok", ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("list failure = %d, want 500", rec.Code)
	}

	plain := &fakeStore{token: &store.Token{Scopes: []string{"read"}}}
	if rec := do(t, New(plain, fakeEmbedder{}, nil), "GET", "/api/v1/runtime-graph", "tok", ""); rec.Code != http.StatusNotImplemented {
		t.Errorf("store without runtime graph = %d, want 501", rec.Code)
	}
}

func TestSubmitRuntimeEdgesErrors(t *testing.T) {
	proj := &store.Project{ID: 3, Name: "repo"}
	body := `{"edges":[{"target_project":"other","protocol":"http"}]}`

	plain := &fakeStore{token: &store.Token{Scopes: []string{"write"}}, project: proj}
	if rec := do(t, New(plain, fakeEmbedder{}, nil), "POST", "/api/v1/projects/repo/runtime-edges", "tok", body); rec.Code != http.StatusNotImplemented {
		t.Errorf("store without runtime graph = %d, want 501", rec.Code)
	}

	st := newRuntimeErrStore(proj, "write")
	srv := New(st, fakeEmbedder{}, nil)
	if rec := do(t, srv, "POST", "/api/v1/projects/repo/runtime-edges", "tok", "not json"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad JSON = %d, want 400", rec.Code)
	}
	if rec := do(t, srv, "POST", "/api/v1/projects/repo/runtime-edges", "tok", `{"edges":[]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("no edges = %d, want 400", rec.Code)
	}

	var big strings.Builder
	big.WriteString(`{"edges":[`)
	for i := 0; i <= maxRuntimeEdgesPerRequest; i++ {
		if i > 0 {
			big.WriteByte(',')
		}
		big.WriteString(`{"target_project":"other","protocol":"http"}`)
	}
	big.WriteString(`]}`)
	if rec := do(t, srv, "POST", "/api/v1/projects/repo/runtime-edges", "tok", big.String()); rec.Code != http.StatusBadRequest {
		t.Errorf("too many edges = %d, want 400", rec.Code)
	}

	missing := newRuntimeErrStore(nil, "write")
	if rec := do(t, New(missing, fakeEmbedder{}, nil), "POST", "/api/v1/projects/ghost/runtime-edges", "tok", body); rec.Code != http.StatusNotFound {
		t.Errorf("missing project = %d, want 404", rec.Code)
	}

	broken := newRuntimeErrStore(proj, "write")
	broken.getErr = errors.New("db down")
	if rec := do(t, New(broken, fakeEmbedder{}, nil), "POST", "/api/v1/projects/repo/runtime-edges", "tok", body); rec.Code != http.StatusInternalServerError {
		t.Errorf("project lookup failure = %d, want 500", rec.Code)
	}

	// NOTE: a failing UpsertRuntimeEdges currently answers 400, not 500 — the
	// handler treats a store write failure as a client error. Asserted as-is so
	// the behaviour is pinned; correcting the status is an API change.
	upsertBroken := newRuntimeErrStore(proj, "write")
	upsertBroken.upsertErr = errors.New("write failed")
	if rec := do(t, New(upsertBroken, fakeEmbedder{}, nil), "POST", "/api/v1/projects/repo/runtime-edges", "tok", body); rec.Code != http.StatusBadRequest {
		t.Errorf("upsert failure = %d, want the current 400", rec.Code)
	}
}

func TestSetProjectPrivacyErrors(t *testing.T) {
	proj := &store.Project{ID: 3, Name: "repo"}

	plain := &fakeStore{token: &store.Token{Scopes: []string{"admin"}}, project: proj}
	if rec := do(t, New(plain, fakeEmbedder{}, nil), "PUT", "/api/v1/projects/repo/privacy", "tok", `{"mode":"edge"}`); rec.Code != http.StatusNotImplemented {
		t.Errorf("store without policies = %d, want 501", rec.Code)
	}

	missing := newRuntimeErrStore(nil, "admin")
	if rec := do(t, New(missing, fakeEmbedder{}, nil), "PUT", "/api/v1/projects/ghost/privacy", "tok", `{"mode":"edge"}`); rec.Code != http.StatusNotFound {
		t.Errorf("missing project = %d, want 404", rec.Code)
	}

	broken := newRuntimeErrStore(proj, "admin")
	broken.getErr = errors.New("db down")
	if rec := do(t, New(broken, fakeEmbedder{}, nil), "PUT", "/api/v1/projects/repo/privacy", "tok", `{"mode":"edge"}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("project lookup failure = %d, want 500", rec.Code)
	}

	st := newRuntimeErrStore(proj, "admin")
	srv := New(st, fakeEmbedder{}, nil)
	if rec := do(t, srv, "PUT", "/api/v1/projects/repo/privacy", "tok", "not json"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad JSON = %d, want 400", rec.Code)
	}
	if rec := do(t, srv, "PUT", "/api/v1/projects/repo/privacy", "tok", `{"mode":"nonsense"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown mode = %d, want 400", rec.Code)
	}

	saveBroken := newRuntimeErrStore(proj, "admin")
	saveBroken.privacyErr = errors.New("write failed")
	if rec := do(t, New(saveBroken, fakeEmbedder{}, nil), "PUT", "/api/v1/projects/repo/privacy", "tok", `{"mode":"edge"}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("save failure = %d, want 500", rec.Code)
	}
}
