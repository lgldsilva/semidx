package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/depcatalog"
	"github.com/lgldsilva/semidx/internal/store"
)

// depJobStore is the job-worker counterpart of depAPIStore: the base fake plus
// the DependencyStore surface the resolve job requires.
type depJobStore struct {
	*fakeStore
	replaced []store.Dependency
	replErr  error
}

func (d *depJobStore) ListProjectDependencies(context.Context, int) ([]store.Dependency, error) {
	return nil, nil
}

func (d *depJobStore) FindProjectsSharingDependency(context.Context, int) ([]store.DependencyUsage, error) {
	return nil, nil
}

func (d *depJobStore) ReplaceProjectDependencies(_ context.Context, _ int, deps []store.Dependency) error {
	if d.replErr != nil {
		return d.replErr
	}
	d.replaced = deps
	return nil
}

func dependencyJob(projectID int) *store.Job {
	return &store.Job{ID: 51, Type: "resolve_dependencies", ProjectID: projectID}
}

func TestRunDependencyResolveJobCompletes(t *testing.T) {
	// An empty source dir carries no manifests, so resolution succeeds with an
	// empty catalog and never shells out to a package manager.
	base := &fakeStore{
		claimJob: dependencyJob(1),
		projByID: &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "path", Path: t.TempDir()},
	}
	st := &depJobStore{fakeStore: base}
	srv := New(st, fakeEmbedder{}, nil)

	if !srv.claimAndRun(context.Background(), "") {
		t.Fatal("expected the dependency job to be claimed")
	}
	if base.failCalled {
		t.Fatalf("unexpected FailJob: %s", base.failMsg)
	}
	if !base.compCalled {
		t.Fatal("expected CompleteJob")
	}
	if len(st.replaced) != 0 {
		t.Errorf("replaced = %+v, want an empty catalog", st.replaced)
	}
}

func TestRunDependencyResolveJobWithoutCatalogStore(t *testing.T) {
	fs := &fakeStore{
		claimJob: dependencyJob(1),
		projByID: &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "path", Path: t.TempDir()},
	}
	srv := New(fs, fakeEmbedder{}, nil)

	if !srv.claimAndRun(context.Background(), "") {
		t.Fatal("expected the dependency job to be claimed")
	}
	if !fs.failCalled || !strings.Contains(fs.failMsg, "dependency catalog unavailable") {
		t.Fatalf("failCalled=%v msg=%q", fs.failCalled, fs.failMsg)
	}
}

func TestRunDependencyResolveJobWithoutLocalSource(t *testing.T) {
	base := &fakeStore{
		claimJob: dependencyJob(1),
		projByID: &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "path", Path: ""},
	}
	srv := New(&depJobStore{fakeStore: base}, fakeEmbedder{}, nil)

	if !srv.claimAndRun(context.Background(), "") {
		t.Fatal("expected the dependency job to be claimed")
	}
	if !base.failCalled || !strings.Contains(base.failMsg, "customer agent") {
		t.Fatalf("failCalled=%v msg=%q, want the customer-agent hint", base.failCalled, base.failMsg)
	}
}

func TestRunDependencyResolveJobStoreFailure(t *testing.T) {
	base := &fakeStore{
		claimJob: dependencyJob(1),
		projByID: &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "path", Path: t.TempDir()},
	}
	st := &depJobStore{fakeStore: base, replErr: errors.New("write failed")}
	srv := New(st, fakeEmbedder{}, nil)

	if !srv.claimAndRun(context.Background(), "") {
		t.Fatal("expected the dependency job to be claimed")
	}
	if !base.failCalled || !strings.Contains(base.failMsg, "store dependencies") {
		t.Fatalf("failCalled=%v msg=%q", base.failCalled, base.failMsg)
	}
}

func TestRunDependencyResolveJobCompleteFailure(t *testing.T) {
	base := &fakeStore{
		claimJob: dependencyJob(1),
		projByID: &store.Project{ID: 1, Name: "p", Model: "bge-m3", SourceType: "path", Path: t.TempDir()},
		compErr:  errors.New("db down"),
	}
	srv := New(&depJobStore{fakeStore: base}, fakeEmbedder{}, nil)

	if !srv.claimAndRun(context.Background(), "") {
		t.Fatal("expected the dependency job to be claimed")
	}
	if !base.failCalled || !strings.Contains(base.failMsg, "complete dependency job") {
		t.Fatalf("failCalled=%v msg=%q", base.failCalled, base.failMsg)
	}
}

func TestDependencyFromCatalogCopiesEveryField(t *testing.T) {
	got := dependencyFromCatalog(depcatalog.Dependency{
		Ecosystem:       depcatalog.Ecosystem("npm"),
		Name:            "React",
		NormalizedName:  "react",
		Constraint:      "^18.0.0",
		ResolvedVersion: "18.3.1",
		Scope:           "prod",
		Source:          "npm ls",
		Manifest:        "package.json",
		Direct:          true,
	})
	want := store.Dependency{
		Ecosystem: "npm", Name: "React", NormalizedName: "react",
		Constraint: "^18.0.0", ResolvedVersion: "18.3.1", Scope: "prod",
		Source: "npm ls", Manifest: "package.json", Direct: true,
	}
	if got != want {
		t.Errorf("dependencyFromCatalog = %+v, want %+v", got, want)
	}
}
