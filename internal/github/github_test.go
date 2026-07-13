package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestClient builds a Client pointed at the given fake server, using its own
// HTTP client so the timeout stays short and the transport is the test server's.
func newTestClient(t *testing.T, srv *httptest.Server, token string) *Client {
	t.Helper()
	return New(token, WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
}

func TestNewDefaults(t *testing.T) {
	c := New("tok")
	if c.baseURL != defaultBaseURL {
		t.Fatalf("baseURL = %q, want %q", c.baseURL, defaultBaseURL)
	}
	if c.token != "tok" {
		t.Fatalf("token = %q, want %q", c.token, "tok")
	}
	if c.httpc == nil || c.httpc.Timeout != 30*time.Second {
		t.Fatalf("default http client not set with 30s timeout: %#v", c.httpc)
	}
}

func TestOptionsIgnoreEmptyAndNil(t *testing.T) {
	// Empty base URL and nil http client must be ignored, keeping the defaults.
	c := New("", WithBaseURL(""), WithHTTPClient(nil))
	if c.baseURL != defaultBaseURL {
		t.Fatalf("empty WithBaseURL should keep default, got %q", c.baseURL)
	}
	if c.httpc == nil || c.httpc.Timeout != 30*time.Second {
		t.Fatalf("nil WithHTTPClient should keep default, got %#v", c.httpc)
	}
}

func TestWithBaseURLTrimsTrailingSlash(t *testing.T) {
	c := New("", WithBaseURL("https://ghe.example.com/api/v3/"))
	if c.baseURL != "https://ghe.example.com/api/v3" {
		t.Fatalf("baseURL = %q, want trailing slash trimmed", c.baseURL)
	}
}

const oneRepoBody = `[
  {
    "full_name": "octocat/hello",
    "name": "hello",
    "owner": {"login": "octocat"},
    "clone_url": "https://github.com/octocat/hello.git",
    "ssh_url": "git@github.com:octocat/hello.git",
    "private": true,
    "fork": false,
    "archived": true,
    "description": "greetings",
    "default_branch": "main",
    "updated_at": "2026-01-02T03:04:05Z"
  }
]`

func TestListUserReposHeadersAndMapping(t *testing.T) {
	var gotAuth, gotAccept, gotVersion, gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotVersion = r.Header.Get("X-GitHub-Api-Version")
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(oneRepoBody))
	}))
	defer srv.Close()

	repos, err := newTestClient(t, srv, "secret-token").ListUserRepos(context.Background())
	if err != nil {
		t.Fatalf("ListUserRepos: %v", err)
	}

	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer secret-token")
	}
	if gotAccept != acceptHeader {
		t.Errorf("Accept = %q, want %q", gotAccept, acceptHeader)
	}
	if gotVersion != apiVersionHeader {
		t.Errorf("X-GitHub-Api-Version = %q, want %q", gotVersion, apiVersionHeader)
	}
	if gotPath != "/user/repos" {
		t.Errorf("path = %q, want /user/repos", gotPath)
	}
	if !strings.Contains(gotQuery, "per_page=100") || !strings.Contains(gotQuery, "sort=updated") {
		t.Errorf("query = %q, want per_page=100 & sort=updated", gotQuery)
	}

	if len(repos) != 1 {
		t.Fatalf("len(repos) = %d, want 1", len(repos))
	}
	r := repos[0]
	if r.Owner != "octocat" {
		t.Errorf("Owner = %q, want octocat (from owner.login)", r.Owner)
	}
	if r.FullName != "octocat/hello" || r.Name != "hello" {
		t.Errorf("FullName/Name = %q/%q", r.FullName, r.Name)
	}
	if r.CloneURL != "https://github.com/octocat/hello.git" {
		t.Errorf("CloneURL = %q", r.CloneURL)
	}
	if r.SSHURL != "git@github.com:octocat/hello.git" {
		t.Errorf("SSHURL = %q", r.SSHURL)
	}
	if !r.Private || r.Fork || !r.Archived {
		t.Errorf("Private/Fork/Archived = %v/%v/%v, want true/false/true", r.Private, r.Fork, r.Archived)
	}
	if r.Description != "greetings" || r.DefaultBranch != "main" || r.UpdatedAt != "2026-01-02T03:04:05Z" {
		t.Errorf("Description/DefaultBranch/UpdatedAt = %q/%q/%q", r.Description, r.DefaultBranch, r.UpdatedAt)
	}
}

func TestNoAuthHeaderWhenTokenEmpty(t *testing.T) {
	var hasAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hasAuth = r.Header["Authorization"]
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv, "").ListUserRepos(context.Background()); err != nil {
		t.Fatalf("ListUserRepos: %v", err)
	}
	if hasAuth {
		t.Fatal("Authorization header must be absent when token is empty")
	}
}

func TestListUserReposPagination(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			// Second (last) page: no Link header.
			_, _ = w.Write([]byte(`[{"full_name":"o/second","name":"second","owner":{"login":"o"}}]`))
			return
		}
		// First page advertises a next page via the Link header.
		w.Header().Set("Link", "<"+srv.URL+"/user/repos?page=2>; rel=\"next\", <"+srv.URL+"/user/repos?page=2>; rel=\"last\"")
		_, _ = w.Write([]byte(`[{"full_name":"o/first","name":"first","owner":{"login":"o"}}]`))
	}))
	defer srv.Close()

	repos, err := newTestClient(t, srv, "t").ListUserRepos(context.Background())
	if err != nil {
		t.Fatalf("ListUserRepos: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("len(repos) = %d, want 2 (accumulated across pages)", len(repos))
	}
	if repos[0].Name != "first" || repos[1].Name != "second" {
		t.Fatalf("pagination order wrong: %q, %q", repos[0].Name, repos[1].Name)
	}
}

func TestListOrgReposPathAndEncoding(t *testing.T) {
	var gotEscapedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscapedPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	// Org name with a space forces URL encoding; verify the wire path is encoded.
	if _, err := newTestClient(t, srv, "t").ListOrgRepos(context.Background(), "acme corp"); err != nil {
		t.Fatalf("ListOrgRepos: %v", err)
	}
	if gotEscapedPath != "/orgs/acme%20corp/repos" {
		t.Fatalf("escaped path = %q, want /orgs/acme%%20corp/repos", gotEscapedPath)
	}
}

func TestListOrgReposSimplePath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(oneRepoBody))
	}))
	defer srv.Close()

	repos, err := newTestClient(t, srv, "t").ListOrgRepos(context.Background(), "octo-org")
	if err != nil {
		t.Fatalf("ListOrgRepos: %v", err)
	}
	if gotPath != "/orgs/octo-org/repos" {
		t.Errorf("path = %q, want /orgs/octo-org/repos", gotPath)
	}
	if len(repos) != 1 || repos[0].Owner != "octocat" {
		t.Errorf("unexpected repos: %+v", repos)
	}
}

func TestErrorStatusesWithGitHubMessage(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantSub []string
	}{
		{"unauthorized", http.StatusUnauthorized, `{"message":"Bad credentials"}`, []string{"401", "Bad credentials"}},
		{"forbidden", http.StatusForbidden, `{"message":"API rate limit exceeded"}`, []string{"403", "API rate limit exceeded"}},
		{"notfound", http.StatusNotFound, `{"message":"Not Found"}`, []string{"404", "Not Found"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			_, err := newTestClient(t, srv, "the-secret").ListUserRepos(context.Background())
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			msg := err.Error()
			for _, sub := range tc.wantSub {
				if !strings.Contains(msg, sub) {
					t.Errorf("error %q missing %q", msg, sub)
				}
			}
			if strings.Contains(msg, "the-secret") {
				t.Errorf("error must not leak the token: %q", msg)
			}
		})
	}
}

func TestErrorNonJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("502 gateway timeout, not json"))
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv, "t").ListUserRepos(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "502") || !strings.Contains(msg, "unexpected response status") {
		t.Fatalf("error = %q, want 502 unexpected response status", msg)
	}
}

func TestDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{not an array`))
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv, "t").ListUserRepos(context.Background())
	if err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("err = %v, want decode response error", err)
	}
}

func TestUnmarshalRepoElementError(t *testing.T) {
	// A non-object array element makes Repo.UnmarshalJSON fail during decoding.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[123]`))
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv, "t").ListUserRepos(context.Background())
	if err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("err = %v, want decode response error from bad element", err)
	}
}

func TestRequestFailedWhenServerClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	c := newTestClient(t, srv, "t")
	srv.Close() // now the transport cannot connect

	_, err := c.ListUserRepos(context.Background())
	if err == nil || !strings.Contains(err.Error(), "request failed") {
		t.Fatalf("err = %v, want request failed", err)
	}
}

func TestInvalidBaseURL(t *testing.T) {
	// A control character makes url.Parse fail inside listRepos.
	c := New("t", WithBaseURL("http://\x7f-bad"))
	_, err := c.ListUserRepos(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid base url") {
		t.Fatalf("err = %v, want invalid base url", err)
	}
}

func TestBadNextPageURL(t *testing.T) {
	// The first page points at a malformed "next" URL, so building the request
	// for page two fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `<http://[>; rel="next"`)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv, "t").ListUserRepos(context.Background())
	if err == nil || !strings.Contains(err.Error(), "build request") {
		t.Fatalf("err = %v, want build request error", err)
	}
}

func TestNextPageURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"next only", `<https://api.github.com/user/repos?page=2>; rel="next"`, "https://api.github.com/user/repos?page=2"},
		{
			"prev and next",
			`<https://api.github.com/user/repos?page=1>; rel="prev", <https://api.github.com/user/repos?page=3>; rel="next", <https://api.github.com/user/repos?page=9>; rel="last"`,
			"https://api.github.com/user/repos?page=3",
		},
		{"no next rel", `<https://api.github.com/user/repos?page=9>; rel="last"`, ""},
		{"unquoted rel", `<https://api.github.com/user/repos?page=2>; rel=next`, "https://api.github.com/user/repos?page=2"},
		{"malformed no semicolon", `<https://api.github.com/user/repos?page=2>`, ""},
		{"whitespace around", `  <https://api.github.com/x?page=2> ;  rel="next" `, "https://api.github.com/x?page=2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextPageURL(tc.in); got != tc.want {
				t.Fatalf("nextPageURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
