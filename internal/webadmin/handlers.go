package webadmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lgldsilva/semidx/internal/passwd"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
)

const (
	tmplLogin         = "login.html"
	msgInternalError  = "internal error"
	headerContentType = "Content-Type"
)

// page is the data every rendered template receives.
type page struct {
	User   *store.User
	CSRF   string
	Active string // nav highlight
	Flash  string // one-off success message
	Err    string // one-off error message
	Data   any    // page-specific payload
}

func (a *Admin) render(w http.ResponseWriter, name string, p page) {
	w.Header().Set(headerContentType, "text/html; charset=utf-8")
	if err := a.tmpl.ExecuteTemplate(w, name, p); err != nil {
		a.log.Error("render failed", "template", name, "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(w, "Internal server error")
	}
}

// --- auth pages --------------------------------------------------------------

func (a *Admin) loginForm(w http.ResponseWriter, r *http.Request) {
	// Already logged in? Go to the dashboard.
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if _, err := a.store.SessionUser(r.Context(), hashToken(cookie.Value)); err == nil {
			http.Redirect(w, r, "/admin/", http.StatusSeeOther)
			return
		}
	}
	a.render(w, tmplLogin, page{Err: r.URL.Query().Get("err")})
}

func (a *Admin) loginSubmit(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	now := time.Now()

	if !a.limiter.allowed(username, now) {
		a.render(w, tmplLogin, page{Err: "too many attempts — wait a few minutes and try again"})
		return
	}

	fail := func() {
		a.limiter.record(username, now)
		a.render(w, tmplLogin, page{Err: "invalid username or password"})
	}

	user, err := a.store.GetUserByUsername(r.Context(), username)
	if errors.Is(err, store.ErrNotFound) || (user != nil && user.Disabled) {
		fail()
		return
	}
	if err != nil {
		a.log.Error("login lookup failed", "err", err)
		http.Error(w, msgInternalError, http.StatusInternalServerError)
		return
	}
	ok, err := passwd.Verify(password, user.PasswordHash)
	if err != nil || !ok {
		fail()
		return
	}

	ttl := sessionTTL
	if r.FormValue("remember_me") == "1" {
		ttl = rememberMeTTL
	}

	plaintext, hash, err := newSessionToken()
	if err != nil {
		http.Error(w, msgInternalError, http.StatusInternalServerError)
		return
	}
	if err := a.store.CreateSession(r.Context(), hash, user.ID, now.Add(ttl)); err != nil {
		a.log.Error("create session failed", "err", err)
		http.Error(w, msgInternalError, http.StatusInternalServerError)
		return
	}
	a.limiter.reset(username)
	a.setSession(w, plaintext, ttl)
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

func (a *Admin) logout(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	if err := a.store.DeleteSession(r.Context(), hashToken(ac.session)); err != nil {
		a.log.Error("delete session failed", "err", err)
	}
	a.clearSession(w)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// --- dashboard & search ------------------------------------------------------

func (a *Admin) dashboard(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	limit, offset := parseListParams(r)
	projects, err := a.store.ListProjects(r.Context(), limit, offset)
	if err != nil {
		a.log.Error("list projects failed", "err", err)
	}
	a.render(w, "dashboard.html", page{User: ac.user, CSRF: ac.csrf, Active: "projects", Data: projects})
}

type adminSearchHit struct {
	Project string
	store.SearchResult
}

type searchData struct {
	Project      string
	AllProjects  bool
	Query        string
	Top          int
	Results      []adminSearchHit
	Fallback     bool
	Ran          bool
	ProjectCount int // set when AllProjects ran
}

func (a *Admin) searchPage(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	topK := 10
	if ts := strings.TrimSpace(r.URL.Query().Get("top")); ts != "" {
		if n, err := strconv.Atoi(ts); err == nil && n > 0 && n <= 100 {
			topK = n
		}
	}
	allProjects := r.URL.Query().Get("all") == "1"
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	d := searchData{
		AllProjects: allProjects,
		Query:       strings.TrimSpace(r.URL.Query().Get("q")),
		Top:         topK,
	}
	if allProjects {
		d.Project = ""
	} else {
		d.Project = project
	}
	p := page{User: ac.user, CSRF: ac.csrf, Active: "search", Data: &d}
	if d.Query == "" {
		a.render(w, "search.html", p)
		return
	}
	if !d.AllProjects && d.Project == "" {
		p.Err = "pick a project or enable “search all projects”"
		a.render(w, "search.html", p)
		return
	}

	d.Ran = true
	if d.AllProjects {
		if err := a.searchAllProjects(r.Context(), &d, topK); err != nil {
			p.Err = err.Error()
		}
	} else {
		resp, err := a.search.Search(r.Context(), search.Request{Project: d.Project, Query: d.Query, TopK: topK})
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				p.Err = "project not found"
			} else {
				p.Err = err.Error()
			}
		} else {
			d.Fallback = resp.Fallback
			for _, hit := range resp.Results {
				d.Results = append(d.Results, adminSearchHit{SearchResult: hit})
			}
		}
	}
	a.render(w, "search.html", p)
}

// searchAllProjects runs the query against every indexed project, merges the hits,
// ranks by score, and keeps the top topK overall (playground-scale corpora).
func (a *Admin) searchAllProjects(ctx context.Context, d *searchData, topK int) error {
	projects, err := a.store.ListProjects(ctx, 0, 0)
	if err != nil {
		a.log.Error("list projects for search failed", "err", err)
		return fmt.Errorf("could not list projects")
	}
	if len(projects) == 0 {
		return fmt.Errorf("no indexed projects")
	}
	d.ProjectCount = len(projects)
	var merged []adminSearchHit
	fallback := false
	for _, proj := range projects {
		resp, serr := a.search.Search(ctx, search.Request{Project: proj.Name, Query: d.Query, TopK: topK})
		if serr != nil {
			if errors.Is(serr, store.ErrNotFound) {
				continue
			}
			return serr
		}
		if resp.Fallback {
			fallback = true
		}
		for _, hit := range resp.Results {
			merged = append(merged, adminSearchHit{Project: proj.Name, SearchResult: hit})
		}
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Score > merged[j].Score })
	if len(merged) > topK {
		merged = merged[:topK]
	}
	d.Results = merged
	d.Fallback = fallback
	return nil
}

type projectItem struct {
	Name   string `json:"name"`
	Model  string `json:"model"`
	Status string `json:"status"`
}

func (a *Admin) projectsAPI(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	limit, offset := parseListParams(r)
	projects, err := a.store.ListProjects(r.Context(), limit, offset)
	if err != nil {
		a.log.Error("list projects (api) failed", "err", err)
		w.Header().Set(headerContentType, "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "internal error"})
		return
	}
	items := make([]projectItem, 0, len(projects))
	for _, p := range projects {
		items = append(items, projectItem{Name: p.Name, Model: p.Model, Status: p.Status})
	}
	if items == nil {
		items = []projectItem{}
	}
	w.Header().Set(headerContentType, "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"projects": items})
}

// --- API keys ----------------------------------------------------------------

type keysData struct {
	Tokens []store.Token
	NewKey string // plaintext of a just-created key, shown once
}

func (a *Admin) keysList(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	a.renderKeys(w, r, ac, "", "", "")
}

func (a *Admin) renderKeys(w http.ResponseWriter, r *http.Request, ac *authCtx, newKey, flash, errMsg string) {
	tokens, err := a.store.ListUserTokens(r.Context(), ac.user.ID, "opaque")
	if err != nil {
		a.log.Error("list tokens failed", "err", err)
		errMsg = "could not load keys"
	}
	a.render(w, "keys.html", page{
		User: ac.user, CSRF: ac.csrf, Active: "keys", Flash: flash, Err: errMsg,
		Data: keysData{Tokens: tokens, NewKey: newKey},
	})
}

func (a *Admin) keysCreate(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		a.renderKeys(w, r, ac, "", "", "a key name is required")
		return
	}
	scopes, err := scopesFromForm(r.Form["scopes"], ac.user.Role)
	if err != nil {
		a.renderKeys(w, r, ac, "", "", err.Error())
		return
	}
	plaintext, hash, err := generateAPIToken()
	if err != nil {
		http.Error(w, msgInternalError, http.StatusInternalServerError)
		return
	}
	if _, err := a.store.CreateUserToken(r.Context(), ac.user.ID, name, hash, scopes); err != nil {
		a.log.Error("create token failed", "err", err)
		a.renderKeys(w, r, ac, "", "", "could not create key")
		return
	}
	a.renderKeys(w, r, ac, plaintext, "Key created — copy it now, it won't be shown again.", "")
}

func (a *Admin) keysRevoke(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	id, err := strconv.Atoi(r.FormValue("id"))
	if err != nil {
		a.renderKeys(w, r, ac, "", "", "invalid key id")
		return
	}
	switch err := a.store.RevokeUserToken(r.Context(), ac.user.ID, id); {
	case errors.Is(err, store.ErrNotFound):
		a.renderKeys(w, r, ac, "", "", "key not found")
	case err != nil:
		a.log.Error("revoke token failed", "err", err)
		a.renderKeys(w, r, ac, "", "", "could not revoke key")
	default:
		a.renderKeys(w, r, ac, "", "Key revoked.", "")
	}
}

// --- account (self-service password change) ----------------------------------

func (a *Admin) accountForm(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	a.render(w, "account.html", page{User: ac.user, CSRF: ac.csrf, Active: "account"})
}

func (a *Admin) accountChangePassword(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	current := r.FormValue("current")
	next := r.FormValue("new")
	render := func(flash, errMsg string) {
		a.render(w, "account.html", page{User: ac.user, CSRF: ac.csrf, Active: "account", Flash: flash, Err: errMsg})
	}
	if ok, _ := passwd.Verify(current, ac.user.PasswordHash); !ok {
		render("", "current password is incorrect")
		return
	}
	if len(next) < 8 {
		render("", "new password must be at least 8 characters")
		return
	}
	hash, err := passwd.Hash(next)
	if err != nil {
		http.Error(w, msgInternalError, http.StatusInternalServerError)
		return
	}
	if err := a.store.SetUserPassword(r.Context(), ac.user.ID, hash); err != nil {
		a.log.Error("set password failed", "err", err)
		render("", "could not update password")
		return
	}
	render("Password updated.", "")
}

// --- users (admin only) ------------------------------------------------------

func (a *Admin) usersList(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	a.renderUsers(w, r, ac, "", "")
}

func (a *Admin) renderUsers(w http.ResponseWriter, r *http.Request, ac *authCtx, flash, errMsg string) {
	limit, offset := parseListParams(r)
	users, err := a.store.ListUsers(r.Context(), limit, offset)
	if err != nil {
		a.log.Error("list users failed", "err", err)
		errMsg = "could not load users"
	}
	a.render(w, "users.html", page{User: ac.user, CSRF: ac.csrf, Active: "users", Flash: flash, Err: errMsg, Data: users})
}

func (a *Admin) usersCreate(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	role := r.FormValue("role")
	if role != "admin" {
		role = "member"
	}
	if username == "" || len(password) < 8 {
		a.renderUsers(w, r, ac, "", "username required and password must be at least 8 characters")
		return
	}
	hash, err := passwd.Hash(password)
	if err != nil {
		http.Error(w, msgInternalError, http.StatusInternalServerError)
		return
	}
	switch _, err := a.store.CreateUser(r.Context(), username, hash, role); {
	case errors.Is(err, store.ErrUserExists):
		a.renderUsers(w, r, ac, "", "a user with that name already exists")
	case err != nil:
		a.log.Error("create user failed", "err", err)
		a.renderUsers(w, r, ac, "", "could not create user")
	default:
		a.renderUsers(w, r, ac, "User "+username+" created.", "")
	}
}

func (a *Admin) usersDisable(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	id, err := strconv.Atoi(r.FormValue("id"))
	if err != nil {
		a.renderUsers(w, r, ac, "", "invalid user id")
		return
	}
	if id == ac.user.ID {
		a.renderUsers(w, r, ac, "", "you cannot disable your own account")
		return
	}
	disabled := r.FormValue("disabled") == "true"
	switch err := a.store.SetUserDisabled(r.Context(), id, disabled); {
	case errors.Is(err, store.ErrNotFound):
		a.renderUsers(w, r, ac, "", "user not found")
	case err != nil:
		a.log.Error("set user disabled failed", "err", err)
		a.renderUsers(w, r, ac, "", "could not update user")
	default:
		a.renderUsers(w, r, ac, "User updated.", "")
	}
}
