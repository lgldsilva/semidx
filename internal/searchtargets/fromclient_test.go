package searchtargets

import (
	"testing"

	"github.com/lgldsilva/semidx/pkg/client"
)

func TestFromClientProjectsCopiesMetadata(t *testing.T) {
	out := FromClientProjects([]client.Project{{
		Name: "p", Model: "m", Status: "ready", SourceType: "git",
		GitURL: "https://x/p.git", Branch: "main",
		Identity: "git:example/p", Path: "/data/p",
	}})
	if len(out) != 1 {
		t.Fatal("expected one project")
	}
	p := out[0]
	if p.Identity != "git:example/p" || p.Path != "/data/p" || p.GitURL != "https://x/p.git" || p.Branch != "main" {
		t.Fatalf("metadata = %+v", p)
	}
}
