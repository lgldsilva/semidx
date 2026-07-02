package main

import (
	"bytes"
	"testing"

	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/pkg/client"
)

// TestRemoteGrepParity is the F5 acceptance check: a server search response, once
// adapted, must render byte-for-byte the same `file:line:content` as an embedded
// search would through the shared GrepFormatter. If this drifts, remote sgrep and
// local sgrep would disagree — the exact regression the plan guards against.
func TestRemoteGrepParity(t *testing.T) {
	remote := &client.SearchResponse{
		Project: "app",
		Model:   "bge-m3",
		Results: []client.SearchHit{
			{Path: "internal/auth/token.go", StartLine: 42, EndLine: 50, Score: 0.91, Content: "func Verify(t string) error {\n\treturn nil\n}"},
			{Path: "cmd/main.go", StartLine: 7, Score: 0.80, Content: "package main"},
		},
	}

	resp := remoteToResponse(remote)

	var got bytes.Buffer
	if err := (search.GrepFormatter{ProjectPath: "/repo"}).Format(&got, resp); err != nil {
		t.Fatal(err)
	}

	want := "/repo/internal/auth/token.go:42:func Verify(t string) error {\n" +
		"/repo/cmd/main.go:7:package main\n"
	if got.String() != want {
		t.Errorf("grep output mismatch:\n got: %q\nwant: %q", got.String(), want)
	}
}

func TestRemoteToResponsePreservesFields(t *testing.T) {
	resp := remoteToResponse(&client.SearchResponse{
		Project:  "p",
		Model:    "m",
		Fallback: true,
		Results:  []client.SearchHit{{Path: "a", StartLine: 1, EndLine: 2, Score: 0.5, Content: "x"}},
	})
	if resp.Project.Name != "p" || resp.Model != "m" || !resp.Fallback {
		t.Errorf("metadata lost: %+v", resp)
	}
	if len(resp.Results) != 1 || resp.Results[0].FilePath != "a" || resp.Results[0].EndLine != 2 {
		t.Errorf("result lost: %+v", resp.Results)
	}
}
