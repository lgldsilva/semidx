package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

// depAPIStore adds the optional DependencyStore surface to the base fake so the
// catalog endpoints can be exercised without PostgreSQL. A plain *fakeStore
// deliberately does NOT implement it, which is how the 501 paths are tested.
type depAPIStore struct {
	*fakeStore
	deps      []store.Dependency
	shared    []store.DependencyUsage
	replaced  []store.Dependency
	listErrD  error
	sharedErr error
	replErr   error
}

func (d *depAPIStore) ListProjectDependencies(context.Context, int) ([]store.Dependency, error) {
	return d.deps, d.listErrD
}

func (d *depAPIStore) FindProjectsSharingDependency(context.Context, int) ([]store.DependencyUsage, error) {
	return d.shared, d.sharedErr
}

func (d *depAPIStore) ReplaceProjectDependencies(_ context.Context, _ int, deps []store.Dependency) error {
	if d.replErr != nil {
		return d.replErr
	}
	d.replaced = deps
	return nil
}

func depServer(t *testing.T, st store.Store) *Server {
	t.Helper()
	return New(st, fakeEmbedder{}, nil)
}

func newDepStore(project *store.Project, scopes ...string) *depAPIStore {
	return &depAPIStore{fakeStore: &fakeStore{
		token:   &store.Token{Scopes: scopes},
		project: project,
	}}
}

func TestListDependencies(t *testing.T) {
	proj := &store.Project{ID: 3, Name: "repo"}
	st := newDepStore(proj, "read")
	st.deps = []store.Dependency{{Ecosystem: "go", Name: "github.com/x/y", NormalizedName: "github.com/x/y"}}

	rec := do(t, depServer(t, st), "GET", "/api/v1/projects/repo/dependencies", "tok", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d, body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Project      string             `json:"project"`
		Dependencies []store.Dependency `json:"dependencies"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if out.Project != "repo" || len(out.Dependencies) != 1 {
		t.Errorf("response = %+v", out)
	}
}

func TestSharedDependencies(t *testing.T) {
	proj := &store.Project{ID: 3, Name: "repo"}
	st := newDepStore(proj, "read")
	st.shared = []store.DependencyUsage{{ProjectID: 9, ProjectName: "other", Name: "github.com/x/y"}}

	rec := do(t, depServer(t, st), "GET", "/api/v1/projects/repo/dependencies/shared", "tok", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("shared = %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"other"`) {
		t.Errorf("body = %s, want the sharing project", rec.Body.String())
	}
}

// Both read endpoints share one failure contract: no catalog surface -> 501,
// unknown project -> 404, project lookup failure -> 500, catalog query failure
// -> 500. Table-driven so the two stay in lockstep.
func TestDependencyReadEndpointsFailureContract(t *testing.T) {
	proj := &store.Project{ID: 3, Name: "repo"}

	endpoints := []struct {
		name      string
		path      string
		ghostPath string
		breakList func(*depAPIStore)
	}{
		{"list", "/api/v1/projects/repo/dependencies", "/api/v1/projects/ghost/dependencies",
			func(d *depAPIStore) { d.listErrD = errors.New("boom") }},
		{"shared", "/api/v1/projects/repo/dependencies/shared", "/api/v1/projects/ghost/dependencies/shared",
			func(d *depAPIStore) { d.sharedErr = errors.New("boom") }},
	}
	for _, ep := range endpoints {
		t.Run(ep.name, func(t *testing.T) {
			// A store without the DependencyStore surface answers 501, not 500.
			plain := &fakeStore{token: &store.Token{Scopes: []string{"read"}}, project: proj}
			if rec := do(t, depServer(t, plain), "GET", ep.path, "tok", ""); rec.Code != http.StatusNotImplemented {
				t.Errorf("no catalog = %d, want 501", rec.Code)
			}

			missing := newDepStore(nil, "read")
			if rec := do(t, depServer(t, missing), "GET", ep.ghostPath, "tok", ""); rec.Code != http.StatusNotFound {
				t.Errorf("missing project = %d, want 404", rec.Code)
			}

			broken := newDepStore(proj, "read")
			broken.getErr = errors.New("db down")
			if rec := do(t, depServer(t, broken), "GET", ep.path, "tok", ""); rec.Code != http.StatusInternalServerError {
				t.Errorf("project lookup failure = %d, want 500", rec.Code)
			}

			queryBroken := newDepStore(proj, "read")
			ep.breakList(queryBroken)
			if rec := do(t, depServer(t, queryBroken), "GET", ep.path, "tok", ""); rec.Code != http.StatusInternalServerError {
				t.Errorf("catalog query failure = %d, want 500", rec.Code)
			}
		})
	}
}

func TestResolveDependenciesManagedQueuesJob(t *testing.T) {
	st := newDepStore(&store.Project{ID: 3, Name: "repo"}, "write")
	st.enqueuedID = 42

	// An empty body defaults to managed mode.
	rec := do(t, depServer(t, st), "POST", "/api/v1/projects/repo/dependencies/resolve", "tok", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("resolve = %d, body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Mode   string `json:"mode"`
		Status string `json:"status"`
		JobID  int    `json:"job_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if out.Mode != "managed" || out.Status != "queued" || out.JobID != 42 {
		t.Errorf("response = %+v", out)
	}
}

func TestResolveDependenciesAgentModeNeverRunsOnServer(t *testing.T) {
	st := newDepStore(&store.Project{ID: 3, Name: "repo"}, "write")
	st.enqueueErr = errors.New("must not enqueue in agent mode")

	rec := do(t, depServer(t, st), "POST", "/api/v1/projects/repo/dependencies/resolve", "tok", `{"mode":"AGENT"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("agent resolve = %d, body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Mode   string `json:"mode"`
		Status string `json:"status"`
		Submit string `json:"submit"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if out.Mode != "agent" || out.Status != "awaiting_agent" {
		t.Errorf("response = %+v", out)
	}
	if out.Submit != "/api/v1/projects/repo/dependencies/submit" {
		t.Errorf("submit endpoint = %q", out.Submit)
	}
}

func TestResolveDependenciesErrors(t *testing.T) {
	proj := &store.Project{ID: 3, Name: "repo"}

	bad := newDepStore(proj, "write")
	if rec := do(t, depServer(t, bad), "POST", "/api/v1/projects/repo/dependencies/resolve", "tok", `{"mode":"whatever"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown mode = %d, want 400", rec.Code)
	}

	missing := newDepStore(nil, "write")
	if rec := do(t, depServer(t, missing), "POST", "/api/v1/projects/ghost/dependencies/resolve", "tok", "{}"); rec.Code != http.StatusNotFound {
		t.Errorf("missing project = %d, want 404", rec.Code)
	}

	broken := newDepStore(proj, "write")
	broken.getErr = errors.New("db down")
	if rec := do(t, depServer(t, broken), "POST", "/api/v1/projects/repo/dependencies/resolve", "tok", "{}"); rec.Code != http.StatusInternalServerError {
		t.Errorf("project lookup failure = %d, want 500", rec.Code)
	}

	enqueueBroken := newDepStore(proj, "write")
	enqueueBroken.enqueueErr = errors.New("queue full")
	if rec := do(t, depServer(t, enqueueBroken), "POST", "/api/v1/projects/repo/dependencies/resolve", "tok", "{}"); rec.Code != http.StatusInternalServerError {
		t.Errorf("enqueue failure = %d, want 500", rec.Code)
	}
}

func TestSubmitDependenciesNormalizesAndStores(t *testing.T) {
	st := newDepStore(&store.Project{ID: 3, Name: "repo"}, "write")

	body := `{"source":"agent-run","dependencies":[
		{"ecosystem":"NPM","name":"React","constraint":"^18.0.0","resolved_version":"18.3.1","direct":true},
		{"ecosystem":"go","name":"github.com/x/y","source":"go list"}
	]}`
	rec := do(t, depServer(t, st), "POST", "/api/v1/projects/repo/dependencies/submit", "tok", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit = %d, body %s", rec.Code, rec.Body.String())
	}
	if len(st.replaced) != 2 {
		t.Fatalf("stored %d dependencies, want 2", len(st.replaced))
	}
	if st.replaced[0].NormalizedName != "react" || st.replaced[0].Ecosystem != "npm" {
		t.Errorf("npm entry not normalized: %+v", st.replaced[0])
	}
	if st.replaced[0].Source != "agent-run" {
		t.Errorf("body-level source not applied: %q", st.replaced[0].Source)
	}
	if st.replaced[1].Source != "go list" {
		t.Errorf("per-item source overridden: %q", st.replaced[1].Source)
	}
}

func TestSubmitDependenciesDefaultsSourceToCustomerAgent(t *testing.T) {
	st := newDepStore(&store.Project{ID: 3, Name: "repo"}, "write")

	rec := do(t, depServer(t, st), "POST", "/api/v1/projects/repo/dependencies/submit", "tok",
		`{"dependencies":[{"ecosystem":"go","name":"github.com/x/y"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit = %d, body %s", rec.Code, rec.Body.String())
	}
	if st.replaced[0].Source != "customer-agent" {
		t.Errorf("source = %q, want customer-agent", st.replaced[0].Source)
	}
}

func TestSubmitDependenciesErrors(t *testing.T) {
	proj := &store.Project{ID: 3, Name: "repo"}

	plain := &fakeStore{token: &store.Token{Scopes: []string{"write"}}, project: proj}
	if rec := do(t, depServer(t, plain), "POST", "/api/v1/projects/repo/dependencies/submit", "tok", "{}"); rec.Code != http.StatusNotImplemented {
		t.Errorf("no catalog = %d, want 501", rec.Code)
	}

	missing := newDepStore(nil, "write")
	if rec := do(t, depServer(t, missing), "POST", "/api/v1/projects/ghost/dependencies/submit", "tok", "{}"); rec.Code != http.StatusNotFound {
		t.Errorf("missing project = %d, want 404", rec.Code)
	}

	broken := newDepStore(proj, "write")
	broken.getErr = errors.New("db down")
	if rec := do(t, depServer(t, broken), "POST", "/api/v1/projects/repo/dependencies/submit", "tok", "{}"); rec.Code != http.StatusInternalServerError {
		t.Errorf("project lookup failure = %d, want 500", rec.Code)
	}

	st := newDepStore(proj, "write")
	srv := depServer(t, st)
	if rec := do(t, srv, "POST", "/api/v1/projects/repo/dependencies/submit", "tok", "not json"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad JSON = %d, want 400", rec.Code)
	}
	if rec := do(t, srv, "POST", "/api/v1/projects/repo/dependencies/submit", "tok",
		`{"dependencies":[{"ecosystem":"cobol","name":"x"}]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unsupported ecosystem = %d, want 400", rec.Code)
	}
	if rec := do(t, srv, "POST", "/api/v1/projects/repo/dependencies/submit", "tok",
		`{"dependencies":[{"ecosystem":"go","name":"   "}]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("empty name = %d, want 400", rec.Code)
	}

	replBroken := newDepStore(proj, "write")
	replBroken.replErr = errors.New("write failed")
	if rec := do(t, depServer(t, replBroken), "POST", "/api/v1/projects/repo/dependencies/submit", "tok",
		`{"dependencies":[{"ecosystem":"go","name":"github.com/x/y"}]}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("store failure = %d, want 500", rec.Code)
	}
}

func TestSubmitDependenciesRejectsOversizedCatalog(t *testing.T) {
	st := newDepStore(&store.Project{ID: 3, Name: "repo"}, "write")

	var b strings.Builder
	b.WriteString(`{"dependencies":[`)
	for i := 0; i < 10001; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"ecosystem":"go","name":"github.com/x/y"}`)
	}
	b.WriteString(`]}`)

	rec := do(t, depServer(t, st), "POST", "/api/v1/projects/repo/dependencies/submit", "tok", b.String())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized catalog = %d, want 400", rec.Code)
	}
	if st.replaced != nil {
		t.Error("oversized catalog must not reach the store")
	}
}
