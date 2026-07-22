package client

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestPlatformMethods(t *testing.T) {
	c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/tenants":
			_ = json.NewEncoder(w).Encode(map[string]any{"tenants": []Tenant{{ID: 1, Slug: "acme", Name: "Acme"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tenants":
			_ = json.NewEncoder(w).Encode(Tenant{ID: 2, Slug: "new", Name: "New"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/workspaces":
			_ = json.NewEncoder(w).Encode(map[string]any{"workspaces": []Workspace{{ID: 3, TenantID: 1, Slug: "platform", Name: "Platform"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/workspaces":
			_ = json.NewEncoder(w).Encode(Workspace{ID: 4, TenantID: 1, Slug: "new", Name: "New"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/usage":
			_ = json.NewEncoder(w).Encode(UsageResponse{Quota: TenantQuota{TenantID: 1, Plan: "pro", MaxProjects: 10}, Usage: TenantUsage{TenantID: 1, Projects: 2}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/runtime-graph":
			if r.URL.Query().Get("limit") != "3" {
				t.Errorf("runtime graph limit = %q, want 3", r.URL.Query().Get("limit"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"edges": []RuntimeEdge{{TargetProjectName: "worker"}}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/api/dependencies":
			_ = json.NewEncoder(w).Encode(map[string]any{"dependencies": []Dependency{{Ecosystem: "go", Name: "example.com/lib"}}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/api/dependencies/shared":
			_ = json.NewEncoder(w).Encode(map[string]any{"dependencies": []DependencyUsage{{ProjectName: "worker", Name: "example.com/lib"}}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/api/runtime-edges":
			_ = json.NewEncoder(w).Encode(map[string]any{"edges": []RuntimeEdge{{TargetProjectName: "worker"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/api/runtime-edges":
			_ = json.NewEncoder(w).Encode(map[string]any{"accepted": 1})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/api/dependencies/resolve":
			_ = json.NewEncoder(w).Encode(DependencyResolveResponse{Project: "api", Mode: "agent", Status: "submit"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/api/dependencies/submit":
			_ = json.NewEncoder(w).Encode(DependencySubmitResponse{Project: "api", Status: "accepted", Count: 1})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/projects/api/privacy":
			_ = json.NewEncoder(w).Encode(Project{Name: "api", PrivacyMode: "edge"})
		default:
			http.NotFound(w, r)
		}
	})
	defer done()

	ctx := context.Background()
	tenants, err := c.ListTenants(ctx)
	if err != nil || len(tenants) != 1 || tenants[0].Slug != "acme" {
		t.Fatalf("ListTenants = %+v, err %v", tenants, err)
	}
	tenant, err := c.CreateTenant(ctx, "new", "New")
	if err != nil || tenant.Slug != "new" {
		t.Fatalf("CreateTenant = %+v, err %v", tenant, err)
	}
	workspaces, err := c.ListWorkspaces(ctx)
	if err != nil || len(workspaces) != 1 || workspaces[0].Slug != "platform" {
		t.Fatalf("ListWorkspaces = %+v, err %v", workspaces, err)
	}
	workspace, err := c.CreateWorkspace(ctx, "new", "New")
	if err != nil || workspace.Slug != "new" {
		t.Fatalf("CreateWorkspace = %+v, err %v", workspace, err)
	}
	usage, err := c.Usage(ctx)
	if err != nil || usage.Quota.Plan != "pro" || usage.Usage.Projects != 2 {
		t.Fatalf("Usage = %+v, err %v", usage, err)
	}

	deps, err := c.ListDependencies(ctx, "api")
	if err != nil || len(deps) != 1 || deps[0].Name != "example.com/lib" {
		t.Fatalf("ListDependencies = %+v, err %v", deps, err)
	}
	shared, err := c.SharedDependencies(ctx, "api")
	if err != nil || len(shared) != 1 || shared[0].ProjectName != "worker" {
		t.Fatalf("SharedDependencies = %+v, err %v", shared, err)
	}
	edges, err := c.ListRuntimeEdges(ctx, "api")
	if err != nil || len(edges) != 1 || edges[0].TargetProjectName != "worker" {
		t.Fatalf("ListRuntimeEdges = %+v, err %v", edges, err)
	}
	accepted, err := c.SubmitRuntimeEdges(ctx, "api", []RuntimeEdge{{TargetProjectName: "worker"}})
	if err != nil || accepted != 1 {
		t.Fatalf("SubmitRuntimeEdges = %d, err %v", accepted, err)
	}
	graph, err := c.ListRuntimeGraph(ctx, 3)
	if err != nil || len(graph) != 1 {
		t.Fatalf("ListRuntimeGraph = %+v, err %v", graph, err)
	}
	resolved, err := c.ResolveDependencies(ctx, "api", "agent")
	if err != nil || resolved.Status != "submit" {
		t.Fatalf("ResolveDependencies = %+v, err %v", resolved, err)
	}
	submitted, err := c.SubmitDependencies(ctx, "api", []Dependency{{Ecosystem: "go", Name: "example.com/lib"}}, "agent")
	if err != nil || submitted.Count != 1 {
		t.Fatalf("SubmitDependencies = %+v, err %v", submitted, err)
	}
	project, err := c.SetProjectPrivacy(ctx, "api", "edge")
	if err != nil || project.PrivacyMode != "edge" {
		t.Fatalf("SetProjectPrivacy = %+v, err %v", project, err)
	}
}
