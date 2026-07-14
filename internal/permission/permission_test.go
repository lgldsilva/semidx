package permission

import (
	"context"
	"testing"
)

func TestAllowAll(t *testing.T) {
	// AllowAll must approve any request, regardless of its content.
	reqs := []Request{
		{},
		{Tool: "index_worktree", Path: "/repo", Detail: "index /repo"},
		{Tool: "server_repo_sync", Path: "git@host:org/repo.git"},
	}
	for _, req := range reqs {
		ok, err := AllowAll(context.Background(), req)
		if err != nil {
			t.Errorf("AllowAll(%+v) err = %v, want nil", req, err)
		}
		if !ok {
			t.Errorf("AllowAll(%+v) = false, want true", req)
		}
	}
}

func TestDenyAll(t *testing.T) {
	// DenyAll must deny any request as a normal denial (nil error — the caller
	// distinguishes "denied" from "the prompt itself failed").
	reqs := []Request{
		{},
		{Tool: "reindex_project", Path: "/repo", Detail: "reindex"},
	}
	for _, req := range reqs {
		ok, err := DenyAll(context.Background(), req)
		if err != nil {
			t.Errorf("DenyAll(%+v) err = %v, want nil", req, err)
		}
		if ok {
			t.Errorf("DenyAll(%+v) = true, want false", req)
		}
	}
}

// Both built-ins must satisfy the Approver contract so surfaces can plug them
// in wherever a custom prompt-based Approver goes.
func TestBuiltinsSatisfyApprover(t *testing.T) {
	for name, approver := range map[string]Approver{"AllowAll": AllowAll, "DenyAll": DenyAll} {
		if _, err := approver(context.Background(), Request{Tool: "t"}); err != nil {
			t.Errorf("%s as Approver returned err = %v, want nil", name, err)
		}
	}
}
