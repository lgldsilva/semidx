package mcpserver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/repotools"
)

func TestResolveProject(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, req, def, want string
	}{
		{"explicit wins over default", "explicit", "default", "explicit"},
		{"empty request uses default", "", "default", "default"},
		{"blank request uses default", "  ", "default", "default"},
		{"no default keeps request", "explicit", "", "explicit"},
		{"neither stays empty", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveProject(tt.req, tt.def); got != tt.want {
				t.Errorf("resolveProject(%q, %q) = %q, want %q", tt.req, tt.def, got, tt.want)
			}
		})
	}
}

func TestProjectToolDescription(t *testing.T) {
	t.Parallel()
	base := "Search a project."
	if got := projectToolDescription(base, ""); got != base {
		t.Errorf("no default should keep the base description; got %q", got)
	}
	got := projectToolDescription(base, "myproj")
	if !strings.HasPrefix(got, base) {
		t.Errorf("description must keep the base text; got %q", got)
	}
	if !strings.Contains(got, `"myproj"`) {
		t.Errorf("description must name the default project; got %q", got)
	}
}

func TestToolDescriptionsMentionDefaultProject(t *testing.T) {
	t.Parallel()
	sess := connectWithOptions(t, &gitStub{}, Options{DefaultProject: "myproj"})
	res, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	projectTools := map[string]bool{
		toolSemanticSearch: true, toolSemanticReindex: true, toolSemanticStatus: true,
		toolRepoWorktrees: true, toolRepoBranches: true, toolRepoStatus: true,
	}
	for _, tool := range res.Tools {
		mentions := strings.Contains(tool.Description, `"myproj"`)
		if projectTools[tool.Name] && !mentions {
			t.Errorf("%s description should mention the default project; got %q", tool.Name, tool.Description)
		}
		if !projectTools[tool.Name] && mentions {
			t.Errorf("%s takes no project; its description must not mention the default: %q", tool.Name, tool.Description)
		}
	}
}

func TestToolDescriptionsWithoutDefaultAreUnchanged(t *testing.T) {
	t.Parallel()
	sess := connectWithOptions(t, &gitStub{}, Options{})
	res, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if strings.Contains(tool.Description, "default project") {
			t.Errorf("%s must not advertise a default project when none is configured: %q", tool.Name, tool.Description)
		}
	}
}

func TestSearchUsesDefaultProjectWhenOmitted(t *testing.T) {
	t.Parallel()
	var got string
	b := &stubBackend{
		searchFunc: func(_ context.Context, project, _, _ string, _ int, _ bool, _ int) (*SearchOutput, error) {
			got = project
			return &SearchOutput{Project: project}, nil
		},
	}
	sess := connectWithOptions(t, b, Options{DefaultProject: "myproj"})

	if _, isErr := callText(t, sess, "semantic_search", map[string]any{"query": "q"}); isErr {
		t.Fatal("unexpected tool error")
	}
	if got != "myproj" {
		t.Errorf("omitted project should resolve to the default; backend saw %q", got)
	}

	if _, isErr := callText(t, sess, "semantic_search", map[string]any{"project": "other", "query": "q"}); isErr {
		t.Fatal("unexpected tool error")
	}
	if got != "other" {
		t.Errorf("explicit project must win over the default; backend saw %q", got)
	}
}

func TestSearchWithoutProjectAndNoDefaultKeepsBehavior(t *testing.T) {
	t.Parallel()
	var got string
	b := &stubBackend{
		searchFunc: func(_ context.Context, project, _, _ string, _ int, _ bool, _ int) (*SearchOutput, error) {
			got = project
			return &SearchOutput{Project: project}, nil
		},
	}
	sess := connectWithOptions(t, b, Options{})
	if _, isErr := callText(t, sess, "semantic_search", map[string]any{"query": "q"}); isErr {
		t.Fatal("unexpected tool error")
	}
	if got != "" {
		t.Errorf("with no default the backend must see the empty project; saw %q", got)
	}
}

// recordingGitStub records the project each git tool received, and fails for
// "ghost" so the handlers' error branches are exercised too.
type recordingGitStub struct {
	stubBackend
	lastProject string
}

func (g *recordingGitStub) record(p string) error {
	g.lastProject = p
	if p == "ghost" {
		return errors.New("project not found")
	}
	return nil
}
func (g *recordingGitStub) Worktrees(_ context.Context, p string) ([]repotools.Worktree, error) {
	if err := g.record(p); err != nil {
		return nil, err
	}
	return []repotools.Worktree{}, nil
}
func (g *recordingGitStub) Branches(_ context.Context, p string, _ bool) ([]repotools.Branch, error) {
	if err := g.record(p); err != nil {
		return nil, err
	}
	return []repotools.Branch{}, nil
}
func (g *recordingGitStub) GitStatus(_ context.Context, p string) (*repotools.RepoStatus, error) {
	if err := g.record(p); err != nil {
		return nil, err
	}
	return &repotools.RepoStatus{}, nil
}

func TestGitToolsUseDefaultProject(t *testing.T) {
	t.Parallel()
	g := &recordingGitStub{}
	sess := connectWithOptions(t, g, Options{DefaultProject: "myproj"})

	for _, tool := range []string{"repo_worktrees", "repo_branches", "repo_status"} {
		g.lastProject = "unset"
		if text, isErr := callText(t, sess, tool, map[string]any{}); isErr {
			t.Fatalf("%s errored: %q", tool, text)
		}
		if g.lastProject != "myproj" {
			t.Errorf("%s omitted project = %q, want the default myproj", tool, g.lastProject)
		}
		// Error branch: an explicit unknown project surfaces in-band.
		if text, isErr := callText(t, sess, tool, map[string]any{"project": "ghost"}); !isErr || !strings.Contains(text, "not found") {
			t.Errorf("%s ghost = %q (isErr=%v), want in-band not-found", tool, text, isErr)
		}
	}
}

func TestStatusAndReindexUseDefaultProject(t *testing.T) {
	t.Parallel()
	var statusProj, reindexProj string
	b := &stubBackend{
		statusFunc: func(_ context.Context, project string) (*StatusInfo, error) {
			statusProj = project
			return &StatusInfo{Name: project}, nil
		},
		reindexFunc: func(_ context.Context, project, _ string) (string, error) {
			reindexProj = project
			return "queued", nil
		},
	}
	sess := connectWithOptions(t, b, Options{DefaultProject: "myproj"})

	if _, isErr := callText(t, sess, "semantic_status", map[string]any{}); isErr {
		t.Fatal("unexpected status error")
	}
	if statusProj != "myproj" {
		t.Errorf("semantic_status default project = %q, want myproj", statusProj)
	}
	if _, isErr := callText(t, sess, "semantic_reindex", map[string]any{}); isErr {
		t.Fatal("unexpected reindex error")
	}
	if reindexProj != "myproj" {
		t.Errorf("semantic_reindex default project = %q, want myproj", reindexProj)
	}
}
