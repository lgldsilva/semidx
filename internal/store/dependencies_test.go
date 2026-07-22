package store

import (
	"context"
	"testing"
)

func TestProjectDependencyCatalogAndSharedProjects(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p1, err := s.UpsertProject(ctx, "api", "/tmp/api", "m", 0)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := s.UpsertProject(ctx, "worker", "/tmp/worker", "m", 0)
	if err != nil {
		t.Fatal(err)
	}
	deps := []Dependency{{Ecosystem: "maven", Name: "org.slf4j:slf4j-api", NormalizedName: "org.slf4j:slf4j-api", Constraint: "2.0.13", Scope: "compile", Direct: true}}
	if err := s.ReplaceProjectDependencies(ctx, p1, deps); err != nil {
		t.Fatal(err)
	}
	deps[0].ResolvedVersion = "2.0.13"
	if err := s.ReplaceProjectDependencies(ctx, p2, deps); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListProjectDependencies(ctx, p1)
	if err != nil || len(got) != 1 || got[0].Name != deps[0].Name {
		t.Fatalf("list = %+v, err=%v", got, err)
	}
	shared, err := s.FindProjectsSharingDependency(ctx, p1)
	if err != nil || len(shared) != 1 || shared[0].ProjectName != "worker" {
		t.Fatalf("shared = %+v, err=%v", shared, err)
	}
}
