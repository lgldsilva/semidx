package projectref

import (
	"context"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

type memIndex struct {
	store.IndexStore
	projects []store.Project
}

func (m *memIndex) GetProject(_ context.Context, name string) (*store.Project, error) {
	for i := range m.projects {
		if m.projects[i].Name == name {
			return &m.projects[i], nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *memIndex) GetProjectByIdentity(_ context.Context, identity string) (*store.Project, error) {
	for i := range m.projects {
		if m.projects[i].Identity == identity {
			return &m.projects[i], nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *memIndex) ListProjects(context.Context, int, int) ([]store.Project, error) {
	return m.projects, nil
}

func TestResolveCaseInsensitiveName(t *testing.T) {
	db := &memIndex{projects: []store.Project{
		{ID: 1, Name: "jackui", Identity: "git:example/jackui", Path: "/data/jackui"},
	}}
	p, err := Resolve(context.Background(), db, "JackUI")
	if err != nil || p == nil || p.Name != "jackui" {
		t.Fatalf("Resolve(JackUI) = %+v, %v", p, err)
	}
}

func TestResolveByIdentityString(t *testing.T) {
	db := &memIndex{projects: []store.Project{
		{ID: 1, Name: "jackui", Identity: "git:example/jackui"},
	}}
	p, err := Resolve(context.Background(), db, "git:example/jackui")
	if err != nil || p == nil || p.Name != "jackui" {
		t.Fatalf("Resolve(identity) = %+v, %v", p, err)
	}
}

func TestResolveInListByPath(t *testing.T) {
	projects := []store.Project{
		{ID: 1, Name: "docs", Identity: "path:/tmp/docs", Path: "/tmp/docs"},
	}
	p, err := ResolveInList(context.Background(), "/tmp/docs", "", projects)
	if err != nil || p == nil || p.Name != "docs" {
		t.Fatalf("ResolveInList(path) = %+v, %v", p, err)
	}
}

func TestUniqueByIdentity(t *testing.T) {
	in := []store.Project{
		{ID: 1, Name: "jackui-a", Identity: "git:example/jackui"},
		{ID: 2, Name: "jackui-b", Identity: "git:example/jackui"},
		{ID: 3, Name: "other", Identity: "git:example/other"},
		{ID: 4, Name: "legacy", Identity: ""},
	}
	out := UniqueByIdentity(in)
	if len(out) != 3 {
		t.Fatalf("UniqueByIdentity len = %d, want 3", len(out))
	}
}
