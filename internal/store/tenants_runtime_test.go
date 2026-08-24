package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/tenant"
)

// uniqueSlug derives a per-test tenant slug. resetIntegrationDB truncates the
// mutable tables but leaves tenants in place (the default tenant must survive),
// so tests that create tenants would otherwise collide across runs.
func uniqueSlug(t *testing.T, prefix string) string {
	t.Helper()
	name := strings.ToLower(t.Name())
	name = strings.NewReplacer("/", "-", "_", "-", ".", "-").Replace(name)
	return prefix + "-" + name
}

// workspaceCtx pins the default tenant and its default workspace: runtime_edges
// carries a workspace_id foreign key, so a context without one cannot insert.
func workspaceCtx(t *testing.T, s *PgStore) context.Context {
	t.Helper()
	ctx := context.Background()
	workspaces, err := s.ListWorkspaces(ctx)
	if err != nil || len(workspaces) == 0 {
		t.Fatalf("default workspaces = %+v, err=%v", workspaces, err)
	}
	return tenant.MustWith(ctx, tenant.Context{ID: tenant.DefaultID, WorkspaceID: workspaces[0].ID})
}

func TestTenantCRUDAndMemberships(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	created, err := s.CreateTenant(ctx, uniqueSlug(t, "acme"), "ACME Inc")
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if created.ID <= 0 {
		t.Fatalf("created tenant = %+v", created)
	}
	if _, err := s.CreateTenant(ctx, created.Slug, "ACME again"); !errors.Is(err, ErrTenantExists) {
		t.Errorf("duplicate slug err = %v, want ErrTenantExists", err)
	}

	byID, err := s.GetTenantByID(ctx, created.ID)
	if err != nil || byID.Slug != created.Slug {
		t.Fatalf("GetTenantByID = %+v, err=%v", byID, err)
	}
	bySlug, err := s.GetTenantBySlug(ctx, created.Slug)
	if err != nil || bySlug.ID != created.ID {
		t.Fatalf("GetTenantBySlug = %+v, err=%v", bySlug, err)
	}
	if _, err := s.GetTenantByID(ctx, 99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing tenant by id err = %v, want ErrNotFound", err)
	}
	if _, err := s.GetTenantBySlug(ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing tenant by slug err = %v, want ErrNotFound", err)
	}

	// CreateTenant seeds a default workspace for the new tenant.
	if _, err := s.GetWorkspaceBySlug(ctx, "default"); err != nil {
		t.Errorf("default workspace missing after CreateTenant: %v", err)
	}

	user, err := s.CreateUser(ctx, "member", "hash", "member")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertMembership(ctx, Membership{TenantID: created.ID, UserID: user.ID, Role: "member"}); err != nil {
		t.Fatalf("UpsertMembership: %v", err)
	}
	// Re-upserting the same pair changes the role rather than failing.
	if err := s.UpsertMembership(ctx, Membership{TenantID: created.ID, UserID: user.ID, Role: "admin"}); err != nil {
		t.Fatalf("UpsertMembership (update): %v", err)
	}
	memberships, err := s.ListMemberships(ctx, user.ID)
	if err != nil || len(memberships) != 1 || memberships[0].Role != "admin" {
		t.Fatalf("memberships = %+v, err=%v", memberships, err)
	}

	if err := s.UpsertMembership(ctx, Membership{TenantID: created.ID, UserID: user.ID, Role: "wizard"}); !errors.Is(err, ErrInvalidRole) {
		t.Errorf("invalid role err = %v, want ErrInvalidRole", err)
	}
	if err := s.UpsertMembership(ctx, Membership{UserID: user.ID, Role: "member"}); err == nil {
		t.Error("membership without a tenant must be rejected")
	}

	allowed, err := s.CanAccessTenant(ctx, user.ID, created.ID)
	if err != nil || !allowed {
		t.Errorf("CanAccessTenant(member) = %v, err=%v", allowed, err)
	}
	if allowed, err := s.CanAccessTenant(ctx, user.ID, 99999); err != nil || allowed {
		t.Errorf("CanAccessTenant(other tenant) = %v, err=%v", allowed, err)
	}
	if allowed, err := s.CanAccessTenant(ctx, 0, created.ID); err != nil || allowed {
		t.Errorf("CanAccessTenant(anonymous) = %v, err=%v", allowed, err)
	}
}

func TestListTenantsScopesByCaller(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	acme, err := s.CreateTenant(ctx, uniqueSlug(t, "acme"), "ACME")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTenant(ctx, uniqueSlug(t, "globex"), "Globex"); err != nil {
		t.Fatal(err)
	}
	user, err := s.CreateUser(ctx, "member", "hash", "member")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertMembership(ctx, Membership{TenantID: acme.ID, UserID: user.ID, Role: "member"}); err != nil {
		t.Fatal(err)
	}

	// A member sees only the tenants they belong to.
	memberCtx := tenant.MustWith(ctx, tenant.Context{ID: acme.ID, UserID: user.ID})
	mine, err := s.ListTenants(memberCtx)
	if err != nil || len(mine) != 1 || mine[0].Slug != acme.Slug {
		t.Fatalf("member tenants = %+v, err=%v", mine, err)
	}

	// A global admin sees every tenant.
	adminCtx := tenant.MustWith(ctx, tenant.Context{ID: acme.ID, UserID: user.ID, GlobalAdmin: true})
	all, err := s.ListTenants(adminCtx)
	if err != nil || len(all) < 2 {
		t.Fatalf("global-admin tenants = %+v, err=%v", all, err)
	}

	// Without a user identity the caller only sees its own active tenant.
	anon, err := s.ListTenants(ctx)
	if err != nil {
		t.Fatalf("anonymous ListTenants: %v", err)
	}
	if len(anon) > 1 {
		t.Errorf("anonymous tenants = %+v, want at most the active one", anon)
	}
}

func TestRuntimeEdgesRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := workspaceCtx(t, s)

	source, err := s.CreateProject(ctx, "api", "bge-m3", "push", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateProject(ctx, "billing", "bge-m3", "push", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}

	edges := []RuntimeEdge{
		{TargetProjectID: target.ID, TargetProjectName: "billing", Protocol: "http",
			Environment: "prod", RequestCount: 10, ErrorCount: 1, P95LatencyMS: 42},
		{TargetProjectName: "stripe.com", Protocol: "https", RequestCount: 5},
	}
	if err := s.UpsertRuntimeEdges(ctx, source.ID, edges); err != nil {
		t.Fatalf("UpsertRuntimeEdges: %v", err)
	}

	got, err := s.ListRuntimeEdges(ctx, source.ID)
	if err != nil || len(got) != 2 {
		t.Fatalf("ListRuntimeEdges = %+v, err=%v", got, err)
	}
	for _, edge := range got {
		if edge.SourceProjectName != "api" || edge.FirstSeen.IsZero() || edge.LastSeen.IsZero() {
			t.Errorf("edge = %+v, want the source name and both timestamps filled", edge)
		}
	}

	portfolio, err := s.ListWorkspaceRuntimeEdges(ctx, 1)
	if err != nil || len(portfolio) != 1 {
		t.Fatalf("workspace edges (limit 1) = %+v, err=%v", portfolio, err)
	}
	if all, err := s.ListWorkspaceRuntimeEdges(ctx, 0); err != nil || len(all) != 2 {
		t.Fatalf("workspace edges (no limit) = %+v, err=%v", all, err)
	}

	// Re-submitting the same edge identity accumulates counters instead of
	// inserting a duplicate row (the ON CONFLICT path).
	if err := s.UpsertRuntimeEdges(ctx, source.ID, edges[:1]); err != nil {
		t.Fatalf("UpsertRuntimeEdges (repeat): %v", err)
	}
	again, err := s.ListRuntimeEdges(ctx, source.ID)
	if err != nil || len(again) != 2 {
		t.Fatalf("edges after repeat = %+v, err=%v", again, err)
	}
	for _, edge := range again {
		if edge.TargetProjectName == "billing" && edge.RequestCount != 20 {
			t.Errorf("request_count = %d, want 20 accumulated", edge.RequestCount)
		}
	}

	// An empty batch is a no-op rather than an error.
	if err := s.UpsertRuntimeEdges(ctx, source.ID, nil); err != nil {
		t.Errorf("empty batch: %v", err)
	}
}

func TestRuntimeEdgeValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := workspaceCtx(t, s)

	project, err := s.CreateProject(ctx, "api", "bge-m3", "push", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		source int
		edge   RuntimeEdge
	}{
		{"no source project", 0, RuntimeEdge{TargetProjectName: "billing"}},
		{"blank target", project.ID, RuntimeEdge{TargetProjectName: "   "}},
		{"negative counters", project.ID, RuntimeEdge{TargetProjectName: "billing", RequestCount: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.UpsertRuntimeEdges(ctx, tc.source, []RuntimeEdge{tc.edge}); err == nil {
				t.Errorf("%s was accepted", tc.name)
			}
		})
	}
}

func TestTenantQuotaAndUsage(t *testing.T) {
	s := newTestStore(t)
	ctx := workspaceCtx(t, s)

	if err := s.SetTenantQuota(ctx, TenantQuota{MaxProjects: 5, MaxRuntimeEdges: 50}); err != nil {
		t.Fatalf("SetTenantQuota: %v", err)
	}
	quota, err := s.GetTenantQuota(ctx)
	if err != nil || quota.MaxProjects != 5 || quota.MaxRuntimeEdges != 50 {
		t.Fatalf("quota = %+v, err=%v", quota, err)
	}
	if quota.Plan != "custom" {
		t.Errorf("plan = %q, want the custom default", quota.Plan)
	}

	// Updating the same tenant overwrites rather than duplicating.
	if err := s.SetTenantQuota(ctx, TenantQuota{Plan: "team", MaxProjects: 9, MaxRuntimeEdges: 90}); err != nil {
		t.Fatalf("SetTenantQuota (update): %v", err)
	}
	if quota, err := s.GetTenantQuota(ctx); err != nil || quota.Plan != "team" || quota.MaxProjects != 9 {
		t.Fatalf("updated quota = %+v, err=%v", quota, err)
	}

	if err := s.SetTenantQuota(ctx, TenantQuota{MaxProjects: -1}); err == nil {
		t.Error("negative limits must be rejected")
	}

	if _, err := s.CreateProject(ctx, "api", "bge-m3", "push", "", "", 0); err != nil {
		t.Fatal(err)
	}
	usage, err := s.GetTenantUsage(ctx)
	if err != nil || usage.Projects != 1 {
		t.Fatalf("usage = %+v, err=%v", usage, err)
	}
}
