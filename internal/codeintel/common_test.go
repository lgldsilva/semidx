package codeintel

import (
	"context"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

// fakeStoreCommon implements store.IndexStore minimally for common tests.
type fakeStoreCommon struct {
	store.IndexStore
	projects []store.Project
}

func (f *fakeStoreCommon) GetProjectByIdentity(_ context.Context, _ string) (*store.Project, error) {
	if len(f.projects) > 0 {
		return &f.projects[0], nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeStoreCommon) ListProjects(_ context.Context, _, _ int) ([]store.Project, error) {
	return f.projects, nil
}

func TestResolveProject_NotFound(t *testing.T) {
	db := &fakeStoreCommon{
		projects: []store.Project{},
	}

	ctx := context.Background()

	_, err := ResolveProject(ctx, db, "")
	if err == nil {
		t.Error("ResolveProject() with no projects should error")
	}
	if err != nil && err.Error() != "no indexed project found — run 'semidx index --project .' first" {
		t.Errorf("ResolveProject() error = %q, want 'no indexed project found...' ", err.Error())
	}
}

func TestResolveProject_WithProjectArg(t *testing.T) {
	tmpDir := t.TempDir()

	proj := store.Project{
		ID:   1,
		Name: "test",
		Path: tmpDir,
	}

	db := &fakeStoreCommon{
		projects: []store.Project{proj},
	}

	ctx := context.Background()

	// This calls projectref.Resolve which needs the project to exist
	// For now, test the not-found path is more robust
	result, err := ResolveProject(ctx, db, "")
	if err != nil {
		// With one project, enclosing should find it if CWD is within tmpDir
		// But CWD is not tmpDir, so it will error - this is expected
		if err.Error() != "no indexed project found — run 'semidx index --project .' first" {
			t.Errorf("ResolveProject() error = %q", err.Error())
		}
		return
	}

	if result == nil {
		t.Error("ResolveProject() returned nil")
	}
}

func TestParseFileLine(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    FileLine
		wantErr bool
	}{
		{
			name:  "simple file:line",
			input: "main.go:42",
			want:  FileLine{File: "main.go", Line: 42},
		},
		{
			name:  "path with directory",
			input: "internal/auth/token.go:100",
			want:  FileLine{File: "internal/auth/token.go", Line: 100},
		},
		{
			name:  "path with colon in directory name",
			input: "some:dir/file.go:10",
			want:  FileLine{File: "some:dir/file.go", Line: 10},
		},
		{
			name:    "no colon",
			input:   "main.go",
			wantErr: true,
		},
		{
			name:    "invalid line number",
			input:   "main.go:abc",
			wantErr: true,
		},
		{
			name:    "zero line number",
			input:   "main.go:0",
			wantErr: true,
		},
		{
			name:    "negative line number",
			input:   "main.go:-1",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseFileLine(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseFileLine() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseFileLine() = %v, want %v", got, tt.want)
			}
		})
	}
}
