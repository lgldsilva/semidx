package store

import (
	"context"
	"testing"

	"github.com/lgldsilva/semidx/internal/tenant"
)

func TestWorkspaceScopesProjectsInsideTenant(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	workspaces, err := s.ListWorkspaces(ctx)
	if err != nil || len(workspaces) == 0 {
		t.Fatalf("default workspaces = %+v, err=%v", workspaces, err)
	}
	isolated, err := s.GetWorkspaceBySlug(ctx, "isolated")
	if err != nil {
		isolated, err = s.CreateWorkspace(ctx, "isolated", "Isolated workspace")
	}
	if err != nil {
		t.Fatal(err)
	}
	defaultCtx := tenant.MustWith(ctx, tenant.Context{ID: tenant.DefaultID, WorkspaceID: workspaces[0].ID})
	isolatedCtx := tenant.MustWith(ctx, tenant.Context{ID: tenant.DefaultID, WorkspaceID: isolated.ID})
	if _, err := s.CreateProject(defaultCtx, "same-name", "m", "push", "", "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProject(isolatedCtx, "same-name", "m", "push", "", "", 0); err != nil {
		t.Fatal(err)
	}
	if p, err := s.GetProject(defaultCtx, "same-name"); err != nil || p.WorkspaceID != workspaces[0].ID {
		t.Fatalf("default project = %+v, err=%v", p, err)
	}
	if p, err := s.GetProject(isolatedCtx, "same-name"); err != nil || p.WorkspaceID != isolated.ID {
		t.Fatalf("isolated project = %+v, err=%v", p, err)
	}
}
