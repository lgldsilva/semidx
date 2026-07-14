package client

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestGitCredentialsCRUD(t *testing.T) {
	var gotID int
	c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/git-credentials":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["host"] != "git.example.com" || body["kind"] != "https" || body["secret"] != "fake-token-value" {
				t.Errorf("create body = %v", body)
			}
			w.WriteHeader(http.StatusCreated)
			gotID = 9
			_ = json.NewEncoder(w).Encode(GitCredential{
				ID: 9, Scope: "host", Host: "git.example.com", Kind: "https", Username: "deploy",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/git-credentials":
			_ = json.NewEncoder(w).Encode(map[string]any{"credentials": []GitCredential{{
				ID: 9, Scope: "host", Host: "git.example.com", Kind: "https", Username: "deploy",
			}}})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/git-credentials/9":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["label"] != "ci" {
				t.Errorf("update body = %v", body)
			}
			_ = json.NewEncoder(w).Encode(GitCredential{ID: 9, Scope: "host", Host: "git.example.com", Kind: "https", Label: "ci"})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/git-credentials/9":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	defer done()

	ctx := context.Background()
	created, err := c.CreateGitCredential(ctx, GitCredentialInput{
		Host: "git.example.com", Kind: "https", Username: "deploy", Secret: "fake-token-value",
	})
	if err != nil || created.ID != 9 {
		t.Fatalf("create = %+v, err %v", created, err)
	}

	list, err := c.ListGitCredentials(ctx)
	if err != nil || len(list) != 1 || list[0].Host != "git.example.com" {
		t.Fatalf("list = %+v, err %v", list, err)
	}

	updated, err := c.UpdateGitCredential(ctx, gotID, GitCredentialUpdateInput{Label: "ci"})
	if err != nil || updated.Label != "ci" {
		t.Fatalf("update = %+v, err %v", updated, err)
	}

	if err := c.DeleteGitCredential(ctx, gotID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestCreateProjectWithInlineCredential(t *testing.T) {
	c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/projects" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		cred, ok := body["credential"].(map[string]any)
		if !ok {
			t.Fatalf("credential missing: %v", body)
		}
		if cred["kind"] != "https" || cred["secret"] != "inline-fake-token" {
			t.Errorf("credential = %v", cred)
		}
		if strings.Contains(mustMarshal(body), "inline-fake-token") == false {
			t.Error("expected secret in request")
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(Project{Name: "priv", SourceType: "git"})
	})
	defer done()

	_, err := c.CreateProjectWithParams(context.Background(), CreateProjectParams{
		Name: "priv", Model: "bge-m3", SourceType: "git", GitURL: "https://git.example.com/r.git",
		Credential: &ProjectCredentialInput{Kind: "https", Secret: "inline-fake-token"},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func mustMarshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestDeleteGitCredentialPath(t *testing.T) {
	c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/git-credentials/"+strconv.Itoa(42) {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	defer done()
	if err := c.DeleteGitCredential(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
}
