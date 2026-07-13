// Package github is a thin, standard-library-only REST client for the GitHub
// API used to DISCOVER repositories a token can reach: the repositories owned by
// (or accessible to) the authenticated user, and the repositories of an
// organization. It performs discovery only — it never clones. Cloning is handled
// elsewhere (see internal/gitsync); this package just lists candidates.
//
// The base URL defaults to https://api.github.com but is overridable via
// WithBaseURL, so the same client works against GitHub Enterprise Server and
// against a fake server in tests.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultBaseURL   = "https://api.github.com"
	apiVersionHeader = "2022-11-28"
	acceptHeader     = "application/vnd.github+json"
	perPage          = "100"

	// maxErrorBody caps how much of a non-2xx response body we read when building
	// an error, so a broken or hostile server cannot make us buffer without bound.
	maxErrorBody = 64 << 10 // 64 KiB
)

// Client is a minimal GitHub REST client for repository discovery. Construct it
// with New; the zero value is not usable.
type Client struct {
	token   string
	baseURL string
	httpc   *http.Client
}

// Option customizes a Client built by New.
type Option func(*Client)

// WithBaseURL overrides the API base URL (default https://api.github.com). Use it
// for GitHub Enterprise Server (e.g. https://ghe.example.com/api/v3) or to point
// tests at an httptest server. An empty value is ignored (keeps the default).
func WithBaseURL(baseURL string) Option {
	return func(c *Client) {
		if baseURL != "" {
			c.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

// WithHTTPClient overrides the underlying *http.Client (default: 30s timeout). A
// nil value is ignored (keeps the default).
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpc = hc
		}
	}
}

// New builds a Client. An empty token produces unauthenticated requests (subject
// to GitHub's low anonymous rate limit); a non-empty token is sent as a bearer
// token on every request.
func New(token string, opts ...Option) *Client {
	c := &Client{
		token:   token,
		baseURL: defaultBaseURL,
		httpc:   &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Repo is a GitHub repository as returned by the repository list endpoints,
// trimmed to the fields semidx needs to present and clone a discovered repo.
type Repo struct {
	FullName      string `json:"full_name"`      // "owner/name"
	Name          string `json:"name"`           // short name
	Owner         string `json:"-"`              // flattened from owner.login
	CloneURL      string `json:"clone_url"`      // https clone URL
	SSHURL        string `json:"ssh_url"`        // git@... clone URL
	Private       bool   `json:"private"`        // private repo
	Fork          bool   `json:"fork"`           // is a fork
	Archived      bool   `json:"archived"`       // read-only/archived
	Description   string `json:"description"`    // may be empty
	DefaultBranch string `json:"default_branch"` // e.g. "main"
	UpdatedAt     string `json:"updated_at"`     // RFC3339 timestamp string
}

// UnmarshalJSON decodes a repository, flattening the nested {"owner":{"login":…}}
// object into the Owner field. It uses an internal alias so decoding does not
// recurse back into this method.
func (r *Repo) UnmarshalJSON(data []byte) error {
	type repoAlias Repo // strips UnmarshalJSON to avoid infinite recursion
	var wire struct {
		repoAlias
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*r = Repo(wire.repoAlias)
	r.Owner = wire.Owner.Login
	return nil
}

// ListUserRepos returns every repository accessible to the authenticated user
// (GET /user/repos), following Link-header pagination to the end.
func (c *Client) ListUserRepos(ctx context.Context) ([]Repo, error) {
	return c.listRepos(ctx, "/user/repos")
}

// ListOrgRepos returns every repository of the given organization
// (GET /orgs/{org}/repos), following Link-header pagination to the end. The org
// name is URL-encoded when the request URL is assembled.
func (c *Client) ListOrgRepos(ctx context.Context, org string) ([]Repo, error) {
	return c.listRepos(ctx, "/orgs/"+org+"/repos")
}

// listRepos assembles the first-page URL for path (with per_page=100&sort=updated)
// and walks the Link header until there is no next page, accumulating all repos.
func (c *Client) listRepos(ctx context.Context, path string) ([]Repo, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("github: invalid base url: %w", err)
	}
	// Setting Path with a decoded value lets url.URL escape it safely on String();
	// no raw string is fed to the HTTP request, so org input cannot break the URL.
	u.Path = strings.TrimRight(u.Path, "/") + path
	q := url.Values{}
	q.Set("per_page", perPage)
	q.Set("sort", "updated")
	u.RawQuery = q.Encode()

	var all []Repo
	next := u.String()
	for next != "" {
		page, link, err := c.getPage(ctx, next)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		next = nextPageURL(link)
	}
	return all, nil
}

// getPage performs a single GET, decoding the repo array and returning the raw
// Link response header so the caller can follow pagination.
func (c *Client) getPage(ctx context.Context, rawURL string) ([]Repo, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("github: build request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("github: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", errorFromResponse(resp)
	}

	var repos []Repo
	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		return nil, "", fmt.Errorf("github: decode response: %w", err)
	}
	return repos, resp.Header.Get("Link"), nil
}

// setHeaders applies the standard GitHub REST headers. The Authorization header
// is only sent when a token is configured.
func (c *Client) setHeaders(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", acceptHeader)
	req.Header.Set("X-GitHub-Api-Version", apiVersionHeader)
}

// errorFromResponse builds an error for a non-2xx response, including the HTTP
// status and, when the body is a GitHub JSON error ({"message": "..."}), the
// GitHub message. It never includes the token.
func errorFromResponse(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	status := fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	if msg := githubMessage(body); msg != "" {
		return fmt.Errorf("github: %s: %s", status, msg)
	}
	return fmt.Errorf("github: unexpected response status %s", status)
}

// githubMessage extracts the "message" field from a GitHub JSON error body,
// returning "" when the body is not such an object.
func githubMessage(body []byte) string {
	var e struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		return ""
	}
	return e.Message
}

// nextPageURL extracts the URL marked rel="next" from a GitHub Link response
// header, returning "" when there is no next page. GitHub formats the header as:
//
//	<https://api.github.com/user/repos?page=2>; rel="next", <...?page=9>; rel="last"
func nextPageURL(linkHeader string) string {
	if linkHeader == "" {
		return ""
	}
	for _, part := range strings.Split(linkHeader, ",") {
		segs := strings.Split(part, ";")
		if len(segs) < 2 {
			continue
		}
		if !hasNextRel(segs[1:]) {
			continue
		}
		target := strings.TrimSpace(segs[0])
		target = strings.TrimPrefix(target, "<")
		target = strings.TrimSuffix(target, ">")
		return target
	}
	return ""
}

// hasNextRel reports whether any of the given Link parameters is rel="next"
// (quotes optional).
func hasNextRel(params []string) bool {
	for _, p := range params {
		switch strings.TrimSpace(p) {
		case `rel="next"`, "rel=next":
			return true
		}
	}
	return false
}
