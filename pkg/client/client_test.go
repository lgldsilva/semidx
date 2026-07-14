package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
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
		if body["keyword"] != false {
			t.Errorf("keyword = %v, want false (semantic search)", body["keyword"])
		}
		_ = json.NewEncoder(w).Encode(SearchResponse{
			Project: "proj", Model: "bge-m3",
			Results: []SearchHit{{Path: "a.go", StartLine: 3, Score: 0.9, Content: "x"}},
		})
	})
	defer done()

	resp, err := c.Search(context.Background(), "proj", "auth", SearchParams{TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Project != "proj" || len(resp.Results) != 1 || resp.Results[0].StartLine != 3 {
		t.Errorf("resp = %+v", resp)
	}
}

// TestSearchKeywordForwarded verifies the client forwards keyword-only mode to
// the server (the remote-mode --keyword path), so an embedding outage never
// blocks a keyword search.
func TestSearchKeywordForwarded(t *testing.T) {
	c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["keyword"] != true {
			t.Errorf("keyword = %v, want true", body["keyword"])
		}
		_ = json.NewEncoder(w).Encode(SearchResponse{Project: "proj", Model: "bge-m3"})
	})
	defer done()

	if _, err := c.Search(context.Background(), "proj", "auth", SearchParams{TopK: 5, Keyword: true}); err != nil {
		t.Fatal(err)
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
		case r.Method == "GET" && r.URL.Path == "/api/v1/projects/p/jobs/7":
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
	job, err := c.GetJob(context.Background(), "p", 7)
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
		w.WriteHeader(http.StatusAccepted)
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

func TestFilesBatchAsyncOldServer(t *testing.T) {
	// Simulate an old server that returns 200 (sync path) without async support.
	c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(BatchResponse{Indexed: 5, Chunks: 10})
	})
	defer done()

	_, err := c.FilesBatchAsync(context.Background(), "p", []BatchFile{{Path: "a.go", Content: "x"}}, nil)
	if err == nil {
		t.Fatal("expected error for old server without async support")
	}
	if !strings.Contains(err.Error(), "--sync") {
		t.Errorf("error should mention --sync: %v", err)
	}
}

func TestWaitForJob(t *testing.T) {
	t.Run("succeeds on first poll", func(t *testing.T) {
		c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			// GET /api/v1/jobs/42 → already done.
			_ = json.NewEncoder(w).Encode(Job{
				ID: 42, Status: JobStatusSucceeded,
				FilesIndexed: 7, ChunksCreated: 42,
				DeletedFiles: 2, ErrorCount: 0,
			})
		})
		defer done()

		job, err := c.WaitForJob(context.Background(), "p", 42, time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if job.FilesIndexed != 7 || job.DeletedFiles != 2 {
			t.Errorf("FilesIndexed=%d DeletedFiles=%d", job.FilesIndexed, job.DeletedFiles)
		}
	})

	t.Run("fails immediately", func(t *testing.T) {
		c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(Job{
				ID: 7, Status: JobStatusFailed,
				Error: "model unavailable",
			})
		})
		defer done()

		job, err := c.WaitForJob(context.Background(), "p", 7, time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status != JobStatusFailed {
			t.Errorf("expected failed, got %s", job.Status)
		}
	})

	t.Run("progresses queued→running→succeeded", func(t *testing.T) {
		var mu sync.Mutex
		calls := 0
		c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			var job Job
			switch n {
			case 1:
				job = Job{ID: 99, Status: JobStatusQueued}
			case 2:
				job = Job{ID: 99, Status: JobStatusRunning}
			default:
				job = Job{ID: 99, Status: JobStatusSucceeded, FilesIndexed: 3, ChunksCreated: 12}
			}
			_ = json.NewEncoder(w).Encode(job)
		})
		defer done()

		job, err := c.WaitForJob(context.Background(), "p", 99, time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if job.FilesIndexed != 3 {
			t.Errorf("FilesIndexed=%d", job.FilesIndexed)
		}
	})

	t.Run("context cancelled", func(t *testing.T) {
		c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(Job{ID: 1, Status: JobStatusQueued})
		})
		defer done()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		time.Sleep(15 * time.Millisecond) // ensure ctx is already expired

		_, err := c.WaitForJob(ctx, "p", 1, time.Second)
		if err == nil {
			t.Fatal("expected error from cancelled context")
		}
	})
}

func TestStatus(t *testing.T) {
	c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if r.Method != "GET" || r.URL.Path != "/api/v1/projects/proj/status" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(StatusResponse{
			Name: "proj", Identity: "git:example/proj", SourceType: "git",
			Status: "ready", Model: "bge-m3", TotalFiles: 42,
		})
	})
	defer done()

	st, err := c.Status(context.Background(), "proj")
	if err != nil {
		t.Fatal(err)
	}
	want := StatusResponse{
		Name: "proj", Identity: "git:example/proj", SourceType: "git",
		Status: "ready", Model: "bge-m3", TotalFiles: 42,
	}
	if *st != want {
		t.Errorf("status = %+v, want %+v", *st, want)
	}
}

func TestStatusNotFound(t *testing.T) {
	c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "project not found"})
	})
	defer done()

	_, err := c.Status(context.Background(), "ghost")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err type = %T, want *APIError", err)
	}
	if apiErr.Status != 404 || !strings.Contains(apiErr.Message, "not found") {
		t.Errorf("apiErr = %+v", apiErr)
	}
}

func TestStatusInvalidJSON(t *testing.T) {
	c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	})
	defer done()

	if _, err := c.Status(context.Background(), "proj"); err == nil {
		t.Fatal("expected a JSON decode error for a malformed body")
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
