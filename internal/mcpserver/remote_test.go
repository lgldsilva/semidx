package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"

	"github.com/lgldsilva/semidx/internal/gitenv"
	"github.com/lgldsilva/semidx/pkg/client"
)

func TestClientBackendResolveCWDProjectByGitOrigin(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", "https://gitea.example/acme/sdk.git"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(gitenv.Clean(os.Environ()), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	t.Chdir(dir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/projects" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"projects": []map[string]any{{
				"name":     "sdk-published",
				"identity": "sdk-published",
				"git_url":  "https://gitea.example/acme/sdk.git",
			}},
		})
	}))
	t.Cleanup(srv.Close)

	resolver, ok := NewClientBackend(client.New(srv.URL, "token")).(cwdProjectResolver)
	if !ok {
		t.Fatal("clientBackend must resolve the current checkout")
	}
	name, err := resolver.ResolveCWDProject(context.Background())
	if err != nil || name != "sdk-published" {
		t.Fatalf("ResolveCWDProject = %q, %v; want sdk-published", name, err)
	}
}
