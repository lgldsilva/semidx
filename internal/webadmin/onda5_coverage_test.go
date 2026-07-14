package webadmin

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func TestWriteScopedJobHappyPath(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3"}}
	fs.jobs = []store.Job{{ID: 7, ProjectID: 1, Status: "running", Type: "full"}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, body := getBody(t, c, srv.URL+"/admin/api/projects/demo/jobs/7")
	if code != 200 || !strings.Contains(body, `"id":7`) || !strings.Contains(body, "running") {
		t.Fatalf("job = %d body=%s", code, body)
	}
}

func TestWriteScopedJobMissing(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3"}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, body := getBody(t, c, srv.URL+"/admin/api/projects/demo/jobs/99")
	if code != 404 || !strings.Contains(body, "not found") {
		t.Fatalf("missing job = %d %s", code, body)
	}
}

func TestConversationsAPI_ErrorBranches(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/api/me")

	code, body := getBody(t, c, srv.URL+"/admin/api/conversations/not-an-id")
	if code != 400 || !strings.Contains(body, "invalid conversation id") {
		t.Fatalf("bad id = %d %s", code, body)
	}
	code, body = getBody(t, c, srv.URL+"/admin/api/conversations/999")
	if code != 404 || !strings.Contains(body, "not found") {
		t.Fatalf("missing = %d %s", code, body)
	}

	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/conversations/1/messages", csrf, map[string]any{
		"role": "assistant", "content": "hi", "sources": []map[string]string{{"path": "a.go"}},
	})
	if code != 404 {
		t.Fatalf("msg on missing conv = %d %s", code, body)
	}
}

type failingConvStore struct {
	*fakeStore
	failList bool
}

func (f *failingConvStore) ListConversations(context.Context, int, int, int) ([]store.Conversation, error) {
	if f.failList {
		return nil, errors.New("boom")
	}
	return f.fakeStore.ListConversations(context.Background(), 0, 0, 0)
}

func TestConversationsAPI_ListFailure(t *testing.T) {
	fs := &failingConvStore{fakeStore: newFakeStore(), failList: true}
	fs.addUser("admin", "supersecret", "admin")
	a, err := New(fs, nil, nil, true, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(a.Handler())
	t.Cleanup(srv.Close)
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	code, body := getBody(t, c, srv.URL+"/admin/api/conversations")
	if code != 500 || !strings.Contains(body, "could not list") {
		t.Fatalf("list fail = %d %s", code, body)
	}
}
