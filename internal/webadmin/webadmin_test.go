package webadmin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/jwtauth"
	"github.com/lgldsilva/semidx/internal/passwd"
	"github.com/lgldsilva/semidx/internal/store"
)

// fakeStore is an in-memory Store covering only what the web admin touches.
type fakeStore struct {
	store.Store
	users             map[int]*store.User
	byName            map[string]*store.User
	sessions          map[string]int // session hash -> user id
	lastSessionExpiry time.Time      // expiry passed to the most recent CreateSession
	tokens            map[int]*store.Token
	tokenOwner        map[int]int
	nextUser          int
	nextTok           int
	projects          []store.Project

	// search support
	searchProject  *store.Project // GetProject result (nil → ErrNotFound)
	searchResults  []store.SearchResult
	searchErr      error               // injected SearchSimilar/Keywords error
	hideIdentities map[string]struct{} // test hook: pretend these identities are gone

	// workspace enrichment
	fileHashes map[string]string // path→hash for ListFileHashes
	fileCount  int
	jobs       []store.Job
	chunks     []store.SearchResult // FetchChunksByPath
	graph      map[string][]string  // FetchGraphNeighbors
	nextJob    int

	// conversation support (ConversationStore)
	convs    map[int]*store.Conversation
	convMsgs map[int][]store.ConversationMessage
	nextConv int
	nextMsg  int

	projectCommit string // GetProjectCommit
	chunkCount    int    // CountProjectChunks

	// error-injection fields (all nil = success/normal path)
	listProjectsErr error
	listTokensErr   error
	createTokErr    error
	createJWTErr    error
	revokeErr       error // generic RevokeUserToken error
	createErr       error // CreateProject error
	listUsersErr    error
	createUserErr   error // generic CreateUser error (distinct from ErrUserExists)
	setPwErr        error
	setDisabledErr  error // generic SetUserDisabled error
	sessionErr      error // generic SessionUser error
	createSessErr   error
	getUserErr      error // generic GetUserByUsername error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		users: map[int]*store.User{}, byName: map[string]*store.User{},
		sessions: map[string]int{}, tokens: map[int]*store.Token{}, tokenOwner: map[int]int{},
		convs: map[int]*store.Conversation{}, convMsgs: map[int][]store.ConversationMessage{},
	}
}

func (f *fakeStore) addUser(username, password, role string) *store.User {
	f.nextUser++
	h, _ := passwd.Hash(password)
	u := &store.User{ID: f.nextUser, Username: username, PasswordHash: h, Role: role}
	f.users[u.ID] = u
	f.byName[username] = u
	return u
}

func (f *fakeStore) GetUserByUsername(_ context.Context, name string) (*store.User, error) {
	if f.getUserErr != nil {
		return nil, f.getUserErr
	}
	if u, ok := f.byName[name]; ok {
		return u, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeStore) GetUserByID(_ context.Context, id int) (*store.User, error) {
	if u, ok := f.users[id]; ok {
		return u, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeStore) CreateUser(_ context.Context, username, hash, role string) (*store.User, error) {
	if f.createUserErr != nil {
		return nil, f.createUserErr
	}
	if _, ok := f.byName[username]; ok {
		return nil, store.ErrUserExists
	}
	f.nextUser++
	u := &store.User{ID: f.nextUser, Username: username, PasswordHash: hash, Role: role}
	f.users[u.ID] = u
	f.byName[username] = u
	return u, nil
}

func (f *fakeStore) ListUsers(context.Context, int, int) ([]store.User, error) {
	if f.listUsersErr != nil {
		return nil, f.listUsersErr
	}
	var out []store.User
	for _, u := range f.users {
		out = append(out, *u)
	}
	return out, nil
}

func (f *fakeStore) SetUserPassword(_ context.Context, id int, hash string) error {
	if f.setPwErr != nil {
		return f.setPwErr
	}
	u, ok := f.users[id]
	if !ok {
		return store.ErrNotFound
	}
	u.PasswordHash = hash
	return nil
}

func (f *fakeStore) SetUserDisabled(_ context.Context, id int, disabled bool) error {
	if f.setDisabledErr != nil {
		return f.setDisabledErr
	}
	u, ok := f.users[id]
	if !ok {
		return store.ErrNotFound
	}
	u.Disabled = disabled
	if disabled {
		for h, uid := range f.sessions {
			if uid == id {
				delete(f.sessions, h)
			}
		}
	}
	return nil
}

func (f *fakeStore) CreateSession(_ context.Context, hash string, userID int, expiresAt time.Time) error {
	if f.createSessErr != nil {
		return f.createSessErr
	}
	f.sessions[hash] = userID
	f.lastSessionExpiry = expiresAt
	return nil
}

func (f *fakeStore) SessionUser(_ context.Context, hash string) (*store.User, error) {
	if f.sessionErr != nil {
		return nil, f.sessionErr
	}
	uid, ok := f.sessions[hash]
	if !ok {
		return nil, store.ErrNotFound
	}
	u := f.users[uid]
	if u == nil || u.Disabled {
		return nil, store.ErrNotFound
	}
	return u, nil
}

func (f *fakeStore) DeleteSession(_ context.Context, hash string) error {
	delete(f.sessions, hash)
	return nil
}

func (f *fakeStore) CreateUserToken(_ context.Context, userID int, name, hash string, scopes []string) (int, error) {
	if f.createTokErr != nil {
		return 0, f.createTokErr
	}
	f.nextTok++
	f.tokens[f.nextTok] = &store.Token{ID: f.nextTok, Name: name, Scopes: scopes, Kind: "opaque", CreatedAt: time.Unix(1, 0)}
	f.tokenOwner[f.nextTok] = userID
	return f.nextTok, nil
}

func (f *fakeStore) CreateJWTToken(_ context.Context, userID int, name, jti string, scopes []string, expiresAt *time.Time) (int, error) {
	if f.createJWTErr != nil {
		return 0, f.createJWTErr
	}
	f.nextTok++
	f.tokens[f.nextTok] = &store.Token{ID: f.nextTok, Name: name, Scopes: scopes, Kind: "jwt", CreatedAt: time.Unix(1, 0), ExpiresAt: expiresAt}
	f.tokenOwner[f.nextTok] = userID
	return f.nextTok, nil
}

func (f *fakeStore) ListUserTokens(_ context.Context, userID int, kind string) ([]store.Token, error) {
	if f.listTokensErr != nil {
		return nil, f.listTokensErr
	}
	var out []store.Token
	for id, owner := range f.tokenOwner {
		if owner == userID && f.tokens[id].Kind == kind {
			out = append(out, *f.tokens[id])
		}
	}
	return out, nil
}

func (f *fakeStore) RevokeUserToken(_ context.Context, userID, id int) error {
	if f.revokeErr != nil {
		return f.revokeErr
	}
	if f.tokenOwner[id] != userID {
		return store.ErrNotFound
	}
	delete(f.tokens, id)
	delete(f.tokenOwner, id)
	return nil
}

func (f *fakeStore) ListProjects(context.Context, int, int) ([]store.Project, error) {
	return f.projects, f.listProjectsErr
}

func (f *fakeStore) GetProject(_ context.Context, name string) (*store.Project, error) {
	for i := range f.projects {
		if f.projects[i].Name == name {
			return &f.projects[i], nil
		}
	}
	if f.searchProject != nil {
		if len(f.projects) == 0 || f.searchProject.Name == name {
			return f.searchProject, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeStore) GetProjectByID(_ context.Context, id int) (*store.Project, error) {
	for i := range f.projects {
		if f.projects[i].ID == id {
			return &f.projects[i], nil
		}
	}
	if f.searchProject != nil && f.searchProject.ID == id {
		return f.searchProject, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeStore) GetProjectByIdentity(_ context.Context, identity string) (*store.Project, error) {
	if _, hide := f.hideIdentities[identity]; hide {
		return nil, store.ErrNotFound
	}
	for i := range f.projects {
		if f.projects[i].Identity == identity {
			return &f.projects[i], nil
		}
	}
	if f.searchProject != nil && f.searchProject.Identity == identity {
		return f.searchProject, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeStore) InsertFileDependencies(context.Context, int, string, []string) error {
	return nil
}

func (f *fakeStore) SearchSimilar(context.Context, int, []float32, int, int) ([]store.SearchResult, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	out := append([]store.SearchResult{}, f.searchResults...)
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}

func (f *fakeStore) SearchSimilarKeywords(context.Context, int, string, int, int) ([]store.SearchResult, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	out := append([]store.SearchResult{}, f.searchResults...)
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}

func (f *fakeStore) CountProjectFiles(context.Context, int) (int, error) {
	if f.fileCount > 0 {
		return f.fileCount, nil
	}
	return len(f.fileHashes), nil
}

func (f *fakeStore) ListFileHashes(context.Context, int) (map[string]string, error) {
	if f.fileHashes == nil {
		return map[string]string{}, nil
	}
	return f.fileHashes, nil
}

func (f *fakeStore) GetProjectCommit(context.Context, int) (string, error) {
	return f.projectCommit, nil
}

func (f *fakeStore) CountProjectChunks(context.Context, int, int) (int, error) {
	return f.chunkCount, nil
}

func (f *fakeStore) ListRecentJobs(_ context.Context, _ int, limit int) ([]store.Job, error) {
	if limit <= 0 || limit > len(f.jobs) {
		return f.jobs, nil
	}
	return f.jobs[:limit], nil
}

func (f *fakeStore) FetchChunksByPath(context.Context, int, string, int, int) ([]store.SearchResult, error) {
	return f.chunks, nil
}

func (f *fakeStore) CreateProject(_ context.Context, name, model, sourceType, gitURL, branch string, dims int) (*store.Project, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	p := store.Project{
		ID: len(f.projects) + 1, Name: name, Model: model, SourceType: sourceType,
		GitURL: gitURL, Branch: branch, Status: "registered", Identity: name, Dims: dims,
	}
	f.projects = append(f.projects, p)
	return &p, nil
}

func (f *fakeStore) DeleteProject(_ context.Context, name string) error {
	for i, p := range f.projects {
		if p.Name == name {
			f.projects = append(f.projects[:i], f.projects[i+1:]...)
			return nil
		}
	}
	return store.ErrNotFound
}

func (f *fakeStore) EnqueueJob(context.Context, int, string) (int, error) {
	f.nextJob++
	return f.nextJob, nil
}

func (f *fakeStore) GetJob(_ context.Context, id int) (*store.Job, error) {
	for i := range f.jobs {
		if f.jobs[i].ID == id {
			return &f.jobs[i], nil
		}
	}
	return &store.Job{ID: id, Status: "queued", Type: "full"}, nil
}

func (f *fakeStore) FetchGraphNeighbors(context.Context, int) (map[string][]string, error) {
	if f.graph != nil {
		return f.graph, nil
	}
	return map[string][]string{}, nil
}

func (f *fakeStore) Close()                                       {}
func (f *fakeStore) Ping(context.Context) error                   { return nil }
func (f *fakeStore) EnsureChunksTable(context.Context, int) error { return nil }
func (f *fakeStore) UpsertProject(context.Context, string, string, string, int) (int, error) {
	return 1, nil
}
func (f *fakeStore) EnsureProjectIdentity(context.Context, string, string, string, string, string, int) (int, error) {
	return 1, nil
}
func (f *fakeStore) SetWorktreeFiles(context.Context, int, string, map[string]string) error {
	return nil
}
func (f *fakeStore) PruneUnreferencedFiles(context.Context, int) (int64, error) { return 0, nil }
func (f *fakeStore) UpdateProjectStatus(_ context.Context, id int, status string) error {
	for i := range f.projects {
		if f.projects[i].ID == id {
			f.projects[i].Status = status
		}
	}
	return nil
}
func (f *fakeStore) UpsertFile(context.Context, int, string, string, int) (int, error) { return 1, nil }
func (f *fakeStore) FileUpToDate(context.Context, int, string, string, int) (bool, error) {
	return false, nil
}
func (f *fakeStore) DeleteFileByPath(_ context.Context, _ int, p string) error {
	delete(f.fileHashes, p)
	return nil
}
func (f *fakeStore) DeleteChunksForFile(context.Context, int, int, int) error { return nil }
func (f *fakeStore) InsertChunks(context.Context, int, int, []chunker.Chunk, [][]float32, int) error {
	return nil
}
func (f *fakeStore) InsertChunksTextOnly(context.Context, int, int, []chunker.Chunk, int) error {
	return nil
}
func (f *fakeStore) SearchSimilarWorktree(ctx context.Context, projectID int, embedding []float32, dims, topK int, worktree string) ([]store.SearchResult, error) {
	return f.SearchSimilar(ctx, projectID, embedding, dims, topK)
}
func (f *fakeStore) SearchSimilarKeywordsWorktree(ctx context.Context, projectID int, queryText string, dims, topK int, worktree string) ([]store.SearchResult, error) {
	return f.SearchSimilarKeywords(ctx, projectID, queryText, dims, topK)
}
func (f *fakeStore) UpdateProjectCommit(context.Context, int, string) error { return nil }
func (f *fakeStore) FetchGraphPathsBFS(context.Context, int, []string, int) (map[string]int, error) {
	return map[string]int{}, nil
}
func (f *fakeStore) EnsureEmbeddingCacheTable(context.Context, int) error { return nil }
func (f *fakeStore) LookupEmbeddingCache(context.Context, []string, string, int) (map[string][]float32, error) {
	return map[string][]float32{}, nil
}
func (f *fakeStore) InsertEmbeddingCache(context.Context, []string, string, [][]float32, int) error {
	return nil
}

// --- helpers -----------------------------------------------------------------

func newTestAdmin(t *testing.T) (*httptest.Server, *fakeStore) {
	t.Helper()
	fs := newFakeStore()
	iss, _ := jwtauth.New("test-secret")
	a, err := New(fs, nil, nil, true, iss, "")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(a.Handler())
	t.Cleanup(srv.Close)
	return srv, fs
}

func newClient(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := srv.Client()
	c.Jar = jar
	c.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse // don't follow redirects, so we can assert them
	}
	return c
}

func login(t *testing.T, c *http.Client, base, user, pass string) {
	t.Helper()
	code, body := postAdminJSON(t, c, base+"/admin/api/login", "", map[string]any{
		"username": user, "password": pass,
	})
	if code != http.StatusOK {
		t.Fatalf("login status = %d; want 200, body=%s", code, body)
	}
}

func extractJSONField(t *testing.T, body, key string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func adminAPIBase(urlStr string) string {
	if i := strings.Index(urlStr, "/admin"); i >= 0 {
		return urlStr[:i+len("/admin")]
	}
	return urlStr
}

func getBody(t *testing.T, c *http.Client, urlStr string) (int, string) {
	t.Helper()
	resp, err := c.Get(urlStr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func csrfFrom(t *testing.T, c *http.Client, urlStr string) string {
	t.Helper()
	_, body := getBody(t, c, adminAPIBase(urlStr)+"/api/me")
	csrf := extractJSONField(t, body, "csrf")
	if csrf == "" {
		t.Fatalf("no csrf token in /admin/api/me")
	}
	return csrf
}

// --- tests -------------------------------------------------------------------

func TestLoginFlowAndSession(t *testing.T) {
	srv, fs := newTestAdmin(t)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)

	// Unauthenticated /admin serves the SPA shell (client-side auth gate).
	if code, body := getBody(t, c, srv.URL+"/admin/"); code != http.StatusOK || !strings.Contains(body, "root") {
		t.Errorf("unauth SPA shell = %d; want 200 with #root", code)
	}
	// Bad password does not create a session.
	code, _ := postAdminJSON(t, c, srv.URL+"/admin/api/login", "", map[string]any{
		"username": "admin", "password": "wrong",
	})
	if code != http.StatusUnauthorized {
		t.Errorf("bad login = %d; want 401", code)
	}
	// Correct login → session → SPA still works.
	login(t, c, srv.URL, "admin", "supersecret")
	if code, body := getBody(t, c, srv.URL+"/admin/"); code != http.StatusOK || !strings.Contains(body, "root") {
		t.Errorf("SPA after login = %d body has root? %v", code, strings.Contains(body, "root"))
	}
}

func TestLegacySettingsRedirects(t *testing.T) {
	srv, fs := newTestAdmin(t)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	for _, path := range []string{"/admin/keys", "/admin/tokens", "/admin/users", "/admin/account"} {
		resp, err := c.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		loc := resp.Header.Get("Location")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther || loc != "/admin/settings" {
			t.Errorf("GET %s = %d Location=%q; want 303 → /admin/settings", path, resp.StatusCode, loc)
		}
	}
}

func TestCSRFRequiredForMutations(t *testing.T) {
	srv, fs := newTestAdmin(t)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, _ := postAdminJSON(t, c, srv.URL+"/admin/api/keys", "", map[string]any{"name": "x"})
	if code != http.StatusForbidden {
		t.Errorf("POST without CSRF = %d; want 403", code)
	}
}

func readAll(resp *http.Response) (int, string) {
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}
