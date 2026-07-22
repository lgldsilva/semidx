package localstore

import (
	"context"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func TestRuntimeGraphPolicyAndQuota(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	ctx := context.Background()
	source, err := s.UpsertProject(ctx, "api", "/tmp/api", "m", 0)
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.UpsertProject(ctx, "worker", "/tmp/worker", "m", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetProjectPrivacy(ctx, source, "edge"); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetProjectByID(ctx, source)
	if err != nil || p.PrivacyMode != "edge" {
		t.Fatalf("privacy = %q, err=%v", p.PrivacyMode, err)
	}
	if err := s.UpsertRuntimeEdges(ctx, source, []store.RuntimeEdge{{
		TargetProjectID: target, TargetProjectName: "worker", Protocol: "grpc",
		Environment: "prod", RequestCount: 2, ErrorCount: 1, P95LatencyMS: 12.5,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertRuntimeEdges(ctx, source, []store.RuntimeEdge{{
		TargetProjectID: target, TargetProjectName: "worker", Protocol: "grpc",
		Environment: "prod", RequestCount: 3, ErrorCount: 0, P95LatencyMS: 10,
	}}); err != nil {
		t.Fatal(err)
	}
	edges, err := s.ListRuntimeEdges(ctx, source)
	if err != nil || len(edges) != 1 || edges[0].RequestCount != 5 || edges[0].ErrorCount != 1 {
		t.Fatalf("runtime edges = %+v, err=%v", edges, err)
	}
	usage, err := s.GetTenantUsage(ctx)
	if err != nil || usage.Projects != 2 || usage.RuntimeEdges != 1 {
		t.Fatalf("usage = %+v, err=%v", usage, err)
	}
	if err := s.SetTenantQuota(ctx, store.TenantQuota{Plan: "starter", MaxProjects: 10, MaxRuntimeEdges: 10}); err != nil {
		t.Fatal(err)
	}
	quota, err := s.GetTenantQuota(ctx)
	if err != nil || quota.Plan != "starter" || quota.MaxProjects != 10 {
		t.Fatalf("quota = %+v, err=%v", quota, err)
	}
}
