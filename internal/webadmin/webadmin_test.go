package webadmin

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

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

	// error-injection fields (all nil = success/normal path)
	listProjectsErr error
	listTokensErr   error
	createTokErr    error
	createJWTErr    error
	revokeErr       error // generic RevokeUserToken error
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
	return f.searchResults, nil
}

func (f *fakeStore) SearchSimilarKeywords(context.Context, int, string, int, int) ([]store.SearchResult, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.searchResults, nil
}

// --- helpers -----------------------------------------------------------------

var csrfRe = regexp.MustCompile(`name="csrf_token" value="([0-9a-f]+)"`)

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
	resp, err := c.PostForm(base+"/admin/login", url.Values{"username": {user}, "password": {pass}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status = %d; want 303", resp.StatusCode)
	}
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
	_, body := getBody(t, c, urlStr)
	m := csrfRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no csrf token in %s", urlStr)
	}
	return m[1]
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
	// Bad password does not create a session (legacy form POST still supported).
	resp, _ := c.PostForm(srv.URL+"/admin/login", url.Values{"username": {"admin"}, "password": {"wrong"}})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("bad login = %d; want 200 (form re-render)", resp.StatusCode)
	}
	// Correct login → session → SPA + keys page still work.
	login(t, c, srv.URL, "admin", "supersecret")
	if code, body := getBody(t, c, srv.URL+"/admin/"); code != http.StatusOK || !strings.Contains(body, "root") {
		t.Errorf("SPA after login = %d body has root? %v", code, strings.Contains(body, "root"))
	}
	if code, body := getBody(t, c, srv.URL+"/admin/keys"); code != http.StatusOK || !strings.Contains(body, "API") {
		t.Errorf("keys page after login = %d", code)
	}
}

func TestCSRFRequiredForMutations(t *testing.T) {
	srv, fs := newTestAdmin(t)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	// POST without a CSRF token is rejected.
	resp, _ := c.PostForm(srv.URL+"/admin/keys", url.Values{"name": {"x"}})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST without CSRF = %d; want 403", resp.StatusCode)
	}
}

func TestAPIKeyLifecycle(t *testing.T) {
	srv, fs := newTestAdmin(t)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")
	resp, _ := c.PostForm(srv.URL+"/admin/keys", url.Values{
		"csrf_token": {csrf}, "name": {"laptop"}, "scopes": {"read", "write"},
	})
	_, body := readAll(resp)
	if !strings.Contains(body, "semidx_") {
		t.Fatalf("created-key page did not show the plaintext key")
	}
	// The key now appears in the list; revoke it.
	if len(fs.tokens) != 1 {
		t.Fatalf("expected 1 token, got %d", len(fs.tokens))
	}
	var id int
	for tid := range fs.tokens {
		id = tid
	}
	csrf = csrfFrom(t, c, srv.URL+"/admin/keys")
	resp, _ = c.PostForm(srv.URL+"/admin/keys/revoke", url.Values{"csrf_token": {csrf}, "id": {strconv.Itoa(id)}})
	_ = resp.Body.Close()
	if len(fs.tokens) != 0 {
		t.Errorf("token not revoked: %d remain", len(fs.tokens))
	}
}

func TestMemberCannotAccessUsers(t *testing.T) {
	srv, fs := newTestAdmin(t)
	fs.addUser("bob", "supersecret", "member")
	c := newClient(t, srv)
	login(t, c, srv.URL, "bob", "supersecret")

	if code, _ := getBody(t, c, srv.URL+"/admin/users"); code != http.StatusForbidden {
		t.Errorf("member GET /admin/users = %d; want 403", code)
	}
}

func TestAdminCreatesMemberAndDisable(t *testing.T) {
	srv, fs := newTestAdmin(t)
	admin := fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	csrf := csrfFrom(t, c, srv.URL+"/admin/users")
	resp, _ := c.PostForm(srv.URL+"/admin/users", url.Values{
		"csrf_token": {csrf}, "username": {"carol"}, "password": {"supersecret"}, "role": {"member"},
	})
	_ = resp.Body.Close()
	if _, ok := fs.byName["carol"]; !ok {
		t.Fatal("member carol was not created")
	}

	// Admin cannot disable their own account.
	csrf = csrfFrom(t, c, srv.URL+"/admin/users")
	resp, _ = c.PostForm(srv.URL+"/admin/users/disable", url.Values{
		"csrf_token": {csrf}, "id": {strconv.Itoa(admin.ID)}, "disabled": {"true"},
	})
	_ = resp.Body.Close()
	if admin.Disabled {
		t.Error("admin disabled their own account — should be blocked")
	}
}

func TestControlTokenLifecycle(t *testing.T) {
	srv, fs := newTestAdmin(t)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	// Issue a non-expiring control token and confirm the JWT is shown once.
	csrf := csrfFrom(t, c, srv.URL+"/admin/tokens")
	resp, _ := c.PostForm(srv.URL+"/admin/tokens", url.Values{
		"csrf_token": {csrf}, "name": {"deploy"}, "scopes": {"read", "write"},
	})
	_, body := readAll(resp)
	// A JWT has three dot-separated segments.
	if strings.Count(firstToken(body), ".") != 2 {
		t.Fatalf("expected a JWT in the response body")
	}
	if len(fs.tokens) != 1 {
		t.Fatalf("expected 1 control token recorded, got %d", len(fs.tokens))
	}

	// It shows under the jwt list, and revoking removes it.
	var id int
	for tid := range fs.tokens {
		id = tid
	}
	csrf = csrfFrom(t, c, srv.URL+"/admin/tokens")
	resp, _ = c.PostForm(srv.URL+"/admin/tokens/revoke", url.Values{"csrf_token": {csrf}, "id": {strconv.Itoa(id)}})
	_ = resp.Body.Close()
	if len(fs.tokens) != 0 {
		t.Errorf("control token not revoked: %d remain", len(fs.tokens))
	}
}

// firstToken returns the first whitespace-delimited chunk containing two dots
// (a heuristic for pulling the JWT out of the rendered page).
func firstToken(body string) string {
	for _, f := range strings.Fields(body) {
		if strings.Count(f, ".") == 2 && len(f) > 40 {
			return f
		}
	}
	return ""
}

func readAll(resp *http.Response) (int, string) {
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}
