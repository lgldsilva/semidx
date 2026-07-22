package localstore

import (
	"context"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func TestDependencyCatalogAndSharedProjects(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	api, err := s.CreateProject(ctx, "api", "bge-m3", "path", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := s.CreateProject(ctx, "worker", "bge-m3", "path", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	dep := store.Dependency{Ecosystem: "go", Name: "example.com/lib", NormalizedName: "example.com/lib", Constraint: "v1", Scope: "runtime", Manifest: "go.mod", Direct: true}
	if err := s.ReplaceProjectDependencies(ctx, api.ID, []store.Dependency{dep}); err != nil {
		t.Fatal(err)
	}
	dep.ResolvedVersion = "v1.2.3"
	if err := s.ReplaceProjectDependencies(ctx, worker.ID, []store.Dependency{dep}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListProjectDependencies(ctx, api.ID)
	if err != nil || len(got) != 1 || got[0].Name != dep.Name {
		t.Fatalf("ListProjectDependencies = %+v, err %v", got, err)
	}
	shared, err := s.FindProjectsSharingDependency(ctx, api.ID)
	if err != nil || len(shared) != 1 || shared[0].ProjectName != "worker" {
		t.Fatalf("FindProjectsSharingDependency = %+v, err %v", shared, err)
	}
}

func TestRuntimeGraphAndQuota(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	api, err := s.CreateProject(ctx, "api", "bge-m3", "path", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	edges := []store.RuntimeEdge{{TargetProjectName: "worker", Protocol: "http", RequestCount: 4, P95LatencyMS: 12.5}}
	if err := s.UpsertRuntimeEdges(ctx, api.ID, edges); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListRuntimeEdges(ctx, api.ID)
	if err != nil || len(got) != 1 || got[0].TargetProjectName != "worker" {
		t.Fatalf("ListRuntimeEdges = %+v, err %v", got, err)
	}
	all, err := s.ListWorkspaceRuntimeEdges(ctx, 1)
	if err != nil || len(all) != 1 {
		t.Fatalf("ListWorkspaceRuntimeEdges = %+v, err %v", all, err)
	}
	if err := s.SetTenantQuota(ctx, store.TenantQuota{Plan: "pro", MaxProjects: 5, MaxRuntimeEdges: 20}); err != nil {
		t.Fatal(err)
	}
	quota, err := s.GetTenantQuota(ctx)
	if err != nil || quota.Plan != "pro" || quota.MaxProjects != 5 {
		t.Fatalf("GetTenantQuota = %+v, err %v", quota, err)
	}
	usage, err := s.GetTenantUsage(ctx)
	if err != nil || usage.Projects != 1 || usage.RuntimeEdges != 1 {
		t.Fatalf("GetTenantUsage = %+v, err %v", usage, err)
	}
}
