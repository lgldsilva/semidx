package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestServer spins an httptest server and returns a Client pointed at it. The
// handler asserts the Bearer token and dispatches on method+path.
func newTestServer(t *testing.T, h http.HandlerFunc) (*Client, func()) {
	t.Helper()
	srv := httptest.NewServer(h)
	return New(srv.URL, "tok-123"), srv.Close
}

func requireAuth(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer tok-123" {
		t.Errorf("Authorization = %q, want Bearer tok-123", got)
	}
}

func TestSearch(t *testing.T) {
	c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if r.Method != "POST" || r.URL.Path != "/api/v1/projects/proj/search" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["query"] != "auth" {
			t.Errorf("query = %v", body["query"])
		}
		_ = json.NewEncoder(w).Encode(SearchResponse{
			Project: "proj", Model: "bge-m3",
			Results: []SearchHit{{Path: "a.go", StartLine: 3, Score: 0.9, Content: "x"}},
		})
	})
	defer done()

	resp, err := c.Search(context.Background(), "proj", "auth", "", 5, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Project != "proj" || len(resp.Results) != 1 || resp.Results[0].StartLine != 3 {
		t.Errorf("resp = %+v", resp)
	}
}

func TestCreateAndListProjects(t *testing.T) {
	c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v1/projects":
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(Project{Name: "repo", SourceType: "git", GitURL: "https://x/y.git"})
		case r.Method == "GET" && r.URL.Path == "/api/v1/projects":
			_ = json.NewEncoder(w).Encode(map[string]any{"projects": []Project{{
				Name: "repo", SourceType: "git", GitURL: "https://x/y.git",
				Identity: "git:example/repo", Path: "/data/repo",
			}}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	defer done()

	p, err := c.CreateProject(context.Background(), "repo", "bge-m3", "git", "https://x/y.git", "")
	if err != nil || p.SourceType != "git" {
		t.Fatalf("create = %+v, err %v", p, err)
	}
	list, err := c.ListProjects(context.Background())
	if err != nil || len(list) != 1 || list[0].Name != "repo" {
		t.Fatalf("list = %+v, err %v", list, err)
	}
	if list[0].Identity != "git:example/repo" || list[0].Path != "/data/repo" {
		t.Fatalf("list metadata = %+v", list[0])
	}
}

func TestEnqueueAndGetJob(t *testing.T) {
	c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v1/projects/p/index-jobs":
			w.WriteHeader(202)
			_ = json.NewEncoder(w).Encode(map[string]any{"job_id": 7, "status": "queued"})
		case r.Method == "GET" && r.URL.Path == "/api/v1/jobs/7":
			_ = json.NewEncoder(w).Encode(Job{ID: 7, Status: "succeeded", FilesIndexed: 4})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	defer done()

	id, err := c.EnqueueJob(context.Background(), "p", "full")
	if err != nil || id != 7 {
		t.Fatalf("enqueue = %d, err %v", id, err)
	}
	job, err := c.GetJob(context.Background(), 7)
	if err != nil || job.Status != "succeeded" || job.FilesIndexed != 4 {
		t.Fatalf("job = %+v, err %v", job, err)
	}
}

func TestFilesDiffAndBatch(t *testing.T) {
	c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/p/files/diff":
			_ = json.NewEncoder(w).Encode(DiffResponse{Stale: []string{"b.go"}, Deleted: []string{"old.go"}})
		case "/api/v1/projects/p/files/batch":
			var body struct {
				Files  []BatchFile `json:"files"`
				Delete []string    `json:"delete"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(BatchResponse{Indexed: len(body.Files), Deleted: len(body.Delete), Chunks: 3})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	defer done()

	diff, err := c.FilesDiff(context.Background(), "p", map[string]string{"a.go": "h1"})
	if err != nil || len(diff.Stale) != 1 || diff.Stale[0] != "b.go" {
		t.Fatalf("diff = %+v, err %v", diff, err)
	}
	batch, err := c.FilesBatch(context.Background(), "p",
		[]BatchFile{{Path: "b.go", Content: "code"}}, []string{"old.go"})
	if err != nil || batch.Indexed != 1 || batch.Deleted != 1 {
		t.Fatalf("batch = %+v, err %v", batch, err)
	}
}

func TestFilesBatchAsync(t *testing.T) {
	c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/projects/p/files/batch" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if sync := r.URL.Query().Get("sync"); sync == "true" {
			t.Error("async batch must not use sync=true")
		}
		_ = json.NewEncoder(w).Encode(EnqueueResponse{JobID: 9, Status: "queued"})
	})
	defer done()

	id, err := c.FilesBatchAsync(context.Background(), "p", []BatchFile{{Path: "a.go", Content: "x"}}, nil)
	if err != nil || id != 9 {
		t.Fatalf("FilesBatchAsync = %d, err %v", id, err)
	}
}

func TestFilesBatchAsyncError(t *testing.T) {
	c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "queue down"})
	})
	defer done()

	if _, err := c.FilesBatchAsync(context.Background(), "p", []BatchFile{{Path: "a.go"}}, nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestAPIErrorMapping(t *testing.T) {
	c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "project not found"})
	})
	defer done()

	_, err := c.GetProject(context.Background(), "ghost")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err type = %T, want *APIError", err)
	}
	if apiErr.Status != 404 || !strings.Contains(apiErr.Message, "not found") {
		t.Errorf("apiErr = %+v", apiErr)
	}
}

func TestDeleteProjectNoContent(t *testing.T) {
	c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("method = %s", r.Method)
		}
		w.WriteHeader(204)
	})
	defer done()
	if err := c.DeleteProject(context.Background(), "p"); err != nil {
		t.Errorf("delete err = %v", err)
	}
}
