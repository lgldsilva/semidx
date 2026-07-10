package webadmin

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/store"
)

func TestValidateIngestBody(t *testing.T) {
	if status, msg := validateIngestBody(&ingestBody{}); msg == "" || status != http.StatusBadRequest {
		t.Fatalf("empty body = %d %q", status, msg)
	}
	files := make([]ingestFile, adminIngestMaxFiles+1)
	if status, msg := validateIngestBody(&ingestBody{Files: files}); !strings.Contains(msg, "too many") {
		t.Fatalf("max files = %d %q", status, msg)
	}
}

func TestCleanRelPath(t *testing.T) {
	if got := cleanRelPath("../etc/passwd"); got != "" {
		t.Fatalf("traversal = %q", got)
	}
	if got := cleanRelPath(`src\a.go`); got != "src/a.go" {
		t.Fatalf("windows = %q", got)
	}
}

func TestIngestIndexOneFile(t *testing.T) {
	fs := newFakeStore()
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3"}}
	idx := indexing.NewIndexer(fs, fakeEmbedder{}, 3, indexing.IndexerOpts{})
	n, ferr, err := ingestIndexOneFile(context.Background(), idx, 1, "m", "main.go", "package main\n")
	if ferr != nil || err != nil || n == 0 {
		t.Fatalf("indexed n=%d ferr=%v err=%v", n, ferr, err)
	}
	_, ferr, _ = ingestIndexOneFile(context.Background(), idx, 1, "m", "../x", "x")
	if ferr == nil {
		t.Fatal("expected invalid path error")
	}
}

func TestReadZipUpload(t *testing.T) {
	_, status, msg := readZipUpload(strings.NewReader("x"), "notes.txt")
	if status != http.StatusBadRequest || !strings.Contains(msg, ".zip") {
		t.Fatalf("non-zip = %d %q", status, msg)
	}
	data := makeTestZip(t, map[string]string{"src/a.go": "package main\n"})
	_, status, msg = readZipUpload(bytes.NewReader(data), "drop.zip")
	if msg != "" {
		t.Fatalf("zip read = %d %q", status, msg)
	}
}

func TestIngestIndexZipEntries(t *testing.T) {
	fs := newFakeStore()
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3"}}
	idx := indexing.NewIndexer(fs, fakeEmbedder{}, 3, indexing.IndexerOpts{})
	data := makeTestZip(t, map[string]string{
		"src/a.go": "package main\n",
		"../x":     "skip",
	})
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	res := ingestIndexZipEntries(context.Background(), testLogger(t), idx, 1, "m", zr)
	if res.indexed != 1 || res.errs == 0 {
		t.Fatalf("indexed=%d errs=%d errors=%v", res.indexed, res.errs, res.fileErrors)
	}
}

func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestIngestBatchAPI(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3", Status: "registered"}}
	fs.fileHashes = map[string]string{"old.go": "dead"}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/projects/demo/files/batch", csrf, map[string]any{
		"files":  []map[string]string{{"path": "main.go", "content": "package main\n"}},
		"delete": []string{"old.go"},
	})
	if code != 200 || !strings.Contains(body, `"indexed":1`) || !strings.Contains(body, `"deleted":1`) {
		t.Fatalf("batch ingest = %d body=%s", code, body)
	}
	if fs.projects[0].Status != "ready" {
		t.Fatalf("status = %q", fs.projects[0].Status)
	}
}

func TestIngestBatchValidation(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3"}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, _ := postAdminJSON(t, c, srv.URL+"/admin/api/projects/demo/files/batch", csrf, map[string]any{})
	if code != 400 {
		t.Fatalf("empty batch = %d", code)
	}
}

func TestIngestNoEmbedder(t *testing.T) {
	srv, fs := newTestAdmin(t)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo"}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/projects/demo/files/batch", csrf, map[string]any{
		"files": []map[string]string{{"path": "a.go", "content": "x"}},
	})
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "no embedder") {
		t.Fatalf("no embedder = %d body=%s", code, body)
	}
}

func makeTestZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
