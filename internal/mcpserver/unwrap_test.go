package mcpserver

import (
	"context"
	"testing"

	"github.com/lgldsilva/semidx/internal/repotools"
	"github.com/lgldsilva/semidx/internal/search"
)

// gitStub is a Backend that also implements GitBackend + MultiSearchBackend.
type gitStub struct{ stubBackend }

func (gitStub) Worktrees(context.Context, string) ([]repotools.Worktree, error) { return nil, nil }
func (gitStub) Branches(context.Context, string, bool) ([]repotools.Branch, error) {
	return nil, nil
}
func (gitStub) GitStatus(context.Context, string) (*repotools.RepoStatus, error) { return nil, nil }
func (gitStub) SearchMulti(context.Context, search.MultiScopeRequest) (*search.MultiResponse, error) {
	return nil, nil
}

// wrapStub embeds a Backend and exposes Unwrap, like the real ask wrappers.
type wrapStub struct{ Backend }

func (w wrapStub) Unwrap() Backend { return w.Backend }

// TestAsGitBackend_seesThroughWrapper is the regression test for the bug where
// wrapping the local backend in an ask backend hid its git / multi-search
// capabilities, so repo_* and semantic_search_multi tools vanished whenever the
// agent (semantic_ask) was enabled.
func TestAsGitBackend_seesThroughWrapper(t *testing.T) {
	git := &gitStub{}

	if _, ok := asGitBackend(git); !ok {
		t.Error("direct GitBackend should be found")
	}
	if _, ok := asGitBackend(wrapStub{Backend: git}); !ok {
		t.Error("GitBackend must be found through a wrapper")
	}
	if _, ok := asMultiSearchBackend(wrapStub{Backend: git}); !ok {
		t.Error("MultiSearchBackend must be found through a wrapper")
	}

	// A plain backend (no git), wrapped or not, must not be seen as GitBackend.
	plain := &stubBackend{}
	if _, ok := asGitBackend(plain); ok {
		t.Error("plain backend has no git capability")
	}
	if _, ok := asGitBackend(wrapStub{Backend: plain}); ok {
		t.Error("wrapping a plain backend must not fabricate git capability")
	}

	// Nil input: both must return false (hits the "b == nil" path out of the loop).
	if _, ok := asGitBackend(nil); ok {
		t.Error("asGitBackend(nil) must return false")
	}
	if _, ok := asMultiSearchBackend(nil); ok {
		t.Error("asMultiSearchBackend(nil) must return false")
	}
}
