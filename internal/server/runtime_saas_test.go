package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

// runtimeQuotaStore keeps the HTTP tests focused on the optional product
// extensions. The embedded fakeStore still supplies the regular Store
// surface, while these methods model a backend that supports the new APIs.
type runtimeQuotaStore struct {
	*fakeStore
	edges  []store.RuntimeEdge
	policy string
	quota  *store.TenantQuota
	usage  *store.TenantUsage
}

func (s *runtimeQuotaStore) SetProjectPrivacy(_ context.Context, _ int, mode string) error {
	s.policy = mode
	return nil
}

func (s *runtimeQuotaStore) UpsertRuntimeEdges(_ context.Context, _ int, edges []store.RuntimeEdge) error {
	s.edges = append(s.edges, edges...)
	return nil
}

func (s *runtimeQuotaStore) ListRuntimeEdges(context.Context, int) ([]store.RuntimeEdge, error) {
	return s.edges, nil
}

func (s *runtimeQuotaStore) ListWorkspaceRuntimeEdges(context.Context, int) ([]store.RuntimeEdge, error) {
	return s.edges, nil
}

func (s *runtimeQuotaStore) GetTenantQuota(context.Context) (*store.TenantQuota, error) {
	return s.quota, nil
}

func (s *runtimeQuotaStore) SetTenantQuota(_ context.Context, quota store.TenantQuota) error {
	s.quota = &quota
	return nil
}

func (s *runtimeQuotaStore) GetTenantUsage(context.Context) (*store.TenantUsage, error) {
	return s.usage, nil
}

func TestRuntimeGraphPrivacyAndUsageEndpoints(t *testing.T) {
	project := &store.Project{ID: 7, Name: "checkout", Model: "bge-m3", PrivacyMode: "hybrid"}
	st := &runtimeQuotaStore{
		fakeStore: &fakeStore{
			token:   &store.Token{Scopes: []string{"read", "write"}},
			project: project,
		},
		edges: []store.RuntimeEdge{{
			SourceProjectID: 7, SourceProjectName: "checkout",
			TargetProjectName: "payments", Protocol: "https", RequestCount: 4,
		}},
		quota: &store.TenantQuota{TenantID: 1, Plan: "team", MaxProjects: 10, MaxRuntimeEdges: 100},
		usage: &store.TenantUsage{TenantID: 1, Projects: 2, RuntimeEdges: 1},
	}
	srv := New(st, fakeEmbedder{}, nil)

	privacyRec := do(t, srv, "PUT", "/api/v1/projects/checkout/privacy", "tok", `{"mode":"edge"}`)
	if privacyRec.Code != 200 || st.policy != "edge" {
		t.Fatalf("privacy update = %d, policy=%q, body=%s", privacyRec.Code, st.policy, privacyRec.Body.String())
	}
	var projectView struct {
		PrivacyMode string `json:"privacy_mode"`
	}
	if err := json.Unmarshal(privacyRec.Body.Bytes(), &projectView); err != nil {
		t.Fatalf("privacy response: %v", err)
	}
	if projectView.PrivacyMode != "edge" {
		t.Fatalf("privacy response mode = %q", projectView.PrivacyMode)
	}

	postRec := do(t, srv, "POST", "/api/v1/projects/checkout/runtime-edges", "tok", `{"edges":[{"target_project":"catalog","source_component":"checkout-api","target_component":"catalog-api","protocol":"grpc","environment":"prod","request_count":3,"error_count":1,"p95_latency_ms":18.5}]}`)
	if postRec.Code != 202 {
		t.Fatalf("runtime submit = %d, body=%s", postRec.Code, postRec.Body.String())
	}
	if len(st.edges) != 2 || st.edges[1].TargetProjectName != "catalog" {
		t.Fatalf("stored runtime edges = %+v", st.edges)
	}

	if rec := do(t, srv, "GET", "/api/v1/projects/checkout/runtime-edges", "tok", ""); rec.Code != 200 {
		t.Fatalf("project runtime graph = %d, body=%s", rec.Code, rec.Body.String())
	}
	if rec := do(t, srv, "GET", "/api/v1/runtime-graph?limit=25", "tok", ""); rec.Code != 200 {
		t.Fatalf("workspace runtime graph = %d, body=%s", rec.Code, rec.Body.String())
	}
	if rec := do(t, srv, "GET", "/api/v1/runtime-graph?limit=nope", "tok", ""); rec.Code != 400 {
		t.Fatalf("invalid runtime graph limit = %d, want 400", rec.Code)
	}

	usageRec := do(t, srv, "GET", "/api/v1/usage", "tok", "")
	if usageRec.Code != 200 {
		t.Fatalf("usage = %d, body=%s", usageRec.Code, usageRec.Body.String())
	}
	var usage struct {
		Quota store.TenantQuota `json:"quota"`
		Usage store.TenantUsage `json:"usage"`
	}
	if err := json.Unmarshal(usageRec.Body.Bytes(), &usage); err != nil {
		t.Fatalf("usage response: %v", err)
	}
	if usage.Quota.Plan != "team" || usage.Usage.RuntimeEdges != 1 {
		t.Fatalf("usage response = %+v", usage)
	}
}

func TestTenantQuotasRejectProjectAndRuntimeGrowth(t *testing.T) {
	st := &runtimeQuotaStore{
		fakeStore: &fakeStore{
			token:   &store.Token{Scopes: []string{"read", "write"}},
			project: &store.Project{ID: 7, Name: "checkout", Model: "bge-m3"},
		},
		quota: &store.TenantQuota{TenantID: 1, Plan: "free", MaxProjects: 1, MaxRuntimeEdges: 1},
		usage: &store.TenantUsage{TenantID: 1, Projects: 1, RuntimeEdges: 1},
	}
	srv := New(st, fakeEmbedder{}, nil)

	projectRec := do(t, srv, "POST", "/api/v1/projects", "tok", `{"name":"new-project","source":{"type":"push"}}`)
	if projectRec.Code != 429 {
		t.Fatalf("project quota = %d, body=%s", projectRec.Code, projectRec.Body.String())
	}
	runtimeRec := do(t, srv, "POST", "/api/v1/projects/checkout/runtime-edges", "tok", `{"edges":[{"target_project":"payments"}]}`)
	if runtimeRec.Code != 429 {
		t.Fatalf("runtime quota = %d, body=%s", runtimeRec.Code, runtimeRec.Body.String())
	}
}
