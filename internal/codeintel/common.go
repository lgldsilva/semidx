package codeintel

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/projectref"
	"github.com/lgldsilva/semidx/internal/store"
)

// FileLine represents a file path and line number reference.
type FileLine struct {
	File string
	Line int
}

// ParseFileLine parses a "file:line" string into a FileLine.
func ParseFileLine(arg string) (FileLine, error) {
	idx := strings.LastIndex(arg, ":")
	if idx < 0 {
		return FileLine{}, fmt.Errorf("expected file:line format, got %q", arg)
	}
	line, err := strconv.Atoi(arg[idx+1:])
	if err != nil || line < 1 {
		return FileLine{}, fmt.Errorf("invalid line number in %q", arg)
	}
	return FileLine{File: arg[:idx], Line: line}, nil
}

// ResolveProject resolves a project for code intelligence operations.
// If projectArg is provided, it uses projectref.Resolve.
// Otherwise, it tries the enclosing git repo, then falls back to listing all
// projects and finding one that encloses the current directory.
func ResolveProject(ctx context.Context, db store.IndexStore, projectArg string) (*store.Project, error) {
	if projectArg != "" {
		return projectref.Resolve(ctx, db, projectArg)
	}
	if gi := gitmeta.Resolve(ctx, "."); gi.IsGit {
		if p, err := db.GetProjectByIdentity(ctx, gi.Identity); err == nil {
			return p, nil
		}
	}
	projects, err := db.ListProjects(ctx, 0, 0)
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if p := projectref.Enclosing(cwd, projects); p != nil {
		return p, nil
	}
	return nil, fmt.Errorf("no indexed project found — run 'semidx index --project .' first")
}
