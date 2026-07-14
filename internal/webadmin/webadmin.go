// Package webadmin is the server's management UI, mounted at /admin by
// `semidx serve`. Product surfaces (projects, search, CLI guide) are a React SPA
// embedded from internal/webui; account/keys/tokens/users remain html/template
// pages. Auth is a cookie-backed server-side session; mutating requests carry a
// CSRF token (form field or X-CSRF-Token header).
package webadmin

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/lgldsilva/semidx/internal/chat"
	embedpkg "github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/jwtauth"
	"github.com/lgldsilva/semidx/internal/rag"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
)

//go:embed templates/*.html
var templatesFS embed.FS

const (
	sessionCookie = "semidx_session"
	sessionTTL    = 24 * time.Hour
	rememberMeTTL = 30 * 24 * time.Hour
	loginWindow   = 15 * time.Minute
	loginMaxTries = 5
)

// ChatOptions carries the per-request chat overrides from the SPA. Zero values
// mean the server defaults; non-zero values are validated by the handlers
// against Config() before reaching the pipeline (unknown mode/model → 400).
type ChatOptions struct {
	Mode  string // "agent" (tool loop) or "rag" (deterministic retrieval)
	Model string // a model id from Config().Models
}

// ChatModelInfo is one selectable chat model in the frozen
// GET /admin/api/chat/config contract.
type ChatModelInfo struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Default  bool   `json:"default"`
}

// ChatConfig is the frozen GET /admin/api/chat/config contract. The frontend
// hides the mode/model selector on 404 or enabled:false.
type ChatConfig struct {
	Enabled      bool            `json:"enabled"`
	Modes        []string        `json:"modes"`
	DefaultMode  string          `json:"default_mode"`
	DefaultModel string          `json:"default_model"`
	Models       []ChatModelInfo `json:"models"`
	AgentActions string          `json:"agent_actions"`
}

// ChatPipeline is the optional chat backend for the project workspace. The
// serve wiring implements it as a per-request factory (mode/model come in via
// opts); nil disables chat in the SPA.
type ChatPipeline interface {
	Ask(ctx context.Context, question, project string, history []chat.Message, opts ChatOptions) (*rag.Answer, error)
	StreamAsk(ctx context.Context, question, project string, history []chat.Message, opts ChatOptions) (<-chan chat.StreamChunk, []rag.Source, string, bool, error)
	// Config reports the chat capability contract served at
	// GET /admin/api/chat/config (modes, model allowlist, agent-actions policy).
	Config() ChatConfig
}

// Admin renders and serves the management UI.
type Admin struct {
	store   store.Store
	emb     embedpkg.Embedder
	search  *search.Service
	tmpl    *template.Template
	log     *slog.Logger
	secure  bool // set the Secure flag on cookies (serve behind HTTPS)
	csrfKey []byte
	limiter *loginLimiter
	jwt     *jwtauth.Issuer // nil when control tokens are disabled
	chat    ChatPipeline    // nil when no chat LLM is configured

	// githubToken enables GitHub repo discovery (list user/org repos) via the
	// admin API; empty disables the feature. githubBaseURL overrides the GitHub
	// API host (GitHub Enterprise, or a test server) — empty uses the default.
	githubToken   string
	githubBaseURL string
}

// New builds the admin UI. secureCookies must be true when the server is reached
// over HTTPS (directly or via a TLS-terminating proxy). jwt may be nil, which
// hides the control-tokens feature.
func New(st store.Store, emb embedpkg.Embedder, log *slog.Logger, secureCookies bool, jwt *jwtauth.Issuer, csrfKeyHex string) (*Admin, error) {
	if log == nil {
		log = slog.New(slog.NewTextHandler(discard{}, nil))
	}
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"fmtTime": func(t time.Time) string {
			if t.IsZero() {
				return "—"
			}
			return t.Format("2006-01-02 15:04")
		},
	}).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	var key []byte
	if csrfKeyHex != "" {
		var err error
		key, err = hex.DecodeString(csrfKeyHex)
		if err != nil {
			return nil, fmt.Errorf("invalid CSRF key: %w", err)
		}
	} else {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
	}
	limiter := &loginLimiter{tries: map[string][]time.Time{}}
	go limiter.reap()
	return &Admin{
		store:   st,
		emb:     emb,
		search:  search.NewService(st, emb),
		tmpl:    tmpl,
		log:     log,
		secure:  secureCookies,
		csrfKey: key,
		limiter: limiter,
		jwt:     jwt,
	}, nil
}

// SetChat enables project workspace chat (RAG). Safe to call with nil to disable.
func (a *Admin) SetChat(p ChatPipeline) { a.chat = p }

// SetGitHub enables GitHub repo discovery via the admin API. token is a GitHub
// PAT; baseURL overrides the API host (empty = the public GitHub API). An empty
// token leaves the feature disabled.
func (a *Admin) SetGitHub(token, baseURL string) {
	a.githubToken = token
	a.githubBaseURL = baseURL
}

// Handler returns a mux serving the /admin/* routes: JSON SPA APIs, legacy
// HTML admin pages (keys/tokens/users), and the embedded React SPA for the
// product surfaces (projects, search, CLI guide).
func (a *Admin) Handler() http.Handler {
	mux := http.NewServeMux()

	// --- SPA JSON API (cookie session + X-CSRF-Token) ---------------------------
	mux.HandleFunc("POST /admin/api/login", a.apiLogin)
	mux.HandleFunc("POST /admin/api/logout", a.protectAPI("", a.apiLogout))
	mux.HandleFunc("GET /admin/api/me", a.protectAPI("", a.apiMe))
	mux.HandleFunc("GET /admin/api/system", a.protectAPI("", a.apiSystem))
	mux.HandleFunc("GET /admin/api/projects", a.protectAPI("", a.projectsAPI))
	mux.HandleFunc("POST /admin/api/projects", a.protectAPI("", a.apiCreateProject))
	mux.HandleFunc("GET /admin/api/projects/{project}", a.protectAPI("", a.apiProjectDetail))
	mux.HandleFunc("DELETE /admin/api/projects/{project}", a.protectAPI("", a.apiDeleteProject))
	mux.HandleFunc("GET /admin/api/projects/{project}/status", a.protectAPI("", a.apiProjectStatus))
	mux.HandleFunc("GET /admin/api/projects/{project}/files", a.protectAPI("", a.apiProjectFiles))
	mux.HandleFunc("GET /admin/api/projects/{project}/files/content", a.protectAPI("", a.apiProjectFileContent))
	mux.HandleFunc("POST /admin/api/projects/{project}/files/batch", a.protectAPI("", a.apiProjectFilesBatch))
	mux.HandleFunc("POST /admin/api/projects/{project}/files/archive", a.protectAPI("", a.apiProjectArchiveBatch))
	mux.HandleFunc("GET /admin/api/projects/{project}/jobs", a.protectAPI("", a.apiProjectJobs))
	mux.HandleFunc("GET /admin/api/projects/{project}/jobs/{id}", a.protectAPI("", a.apiProjectJob))
	mux.HandleFunc("POST /admin/api/projects/{project}/reindex", a.protectAPI("", a.apiReindex))
	mux.HandleFunc("POST /admin/api/projects/{project}/chat", a.protectAPI("", a.apiProjectChat))
	mux.HandleFunc("POST /admin/api/projects/{project}/chat/stream", a.protectAPI("", a.apiProjectChatStream))
	mux.HandleFunc("GET /admin/api/projects/{project}/analyze/explain", a.protectAPI("", a.apiProjectExplain))
	mux.HandleFunc("GET /admin/api/jobs", a.protectAPI("", a.apiListAllJobs))
	mux.HandleFunc("GET /admin/api/jobs/{id}", a.protectAPI("", a.apiGetJob))
	mux.HandleFunc("POST /admin/api/search", a.protectAPI("", a.apiSearch))
	mux.HandleFunc("GET /admin/api/github/repos", a.protectAPI("admin", a.apiGithubRepos))
	// Global (cross-project) chat: no project binding — searches all projects.
	mux.HandleFunc("POST /admin/api/chat", a.protectAPI("", a.apiGlobalChat))
	mux.HandleFunc("POST /admin/api/chat/stream", a.protectAPI("", a.apiGlobalChatStream))
	// Chat capability contract (modes, model allowlist) for the SPA selector.
	mux.HandleFunc("GET /admin/api/chat/config", a.protectAPI("", a.apiChatConfig))
	// Persistent conversations (per-user; PgStore only).
	mux.HandleFunc("GET /admin/api/conversations", a.protectAPI("", a.apiListConversations))
	mux.HandleFunc("POST /admin/api/conversations", a.protectAPI("", a.apiCreateConversation))
	mux.HandleFunc("GET /admin/api/conversations/{id}", a.protectAPI("", a.apiGetConversation))
	mux.HandleFunc("PATCH /admin/api/conversations/{id}", a.protectAPI("", a.apiRenameConversation))
	mux.HandleFunc("DELETE /admin/api/conversations/{id}", a.protectAPI("", a.apiDeleteConversation))
	mux.HandleFunc("POST /admin/api/conversations/{id}/messages", a.protectAPI("", a.apiAddConversationMessage))

	// Settings
	mux.HandleFunc("GET /admin/api/keys", a.protectAPI("", a.apiListKeys))
	mux.HandleFunc("POST /admin/api/keys", a.protectAPI("", a.apiCreateKey))
	mux.HandleFunc("DELETE /admin/api/keys/{id}", a.protectAPI("", a.apiRevokeKey))
	mux.HandleFunc("GET /admin/api/tokens", a.protectAPI("", a.apiListTokens))
	mux.HandleFunc("POST /admin/api/tokens", a.protectAPI("", a.apiCreateToken))
	mux.HandleFunc("DELETE /admin/api/tokens/{id}", a.protectAPI("", a.apiRevokeToken))
	mux.HandleFunc("POST /admin/api/account/password", a.protectAPI("", a.apiChangePassword))
	mux.HandleFunc("GET /admin/api/users", a.protectAPI("admin", a.apiListUsers))
	mux.HandleFunc("POST /admin/api/users", a.protectAPI("admin", a.apiCreateUser))
	mux.HandleFunc("POST /admin/api/users/{id}/disabled", a.protectAPI("admin", a.apiSetUserDisabled))

	// Analyze
	mux.HandleFunc("GET /admin/api/projects/{project}/callers", a.protectAPI("", a.apiProjectCallers))
	mux.HandleFunc("GET /admin/api/projects/{project}/deps", a.protectAPI("", a.apiProjectDeps))
	mux.HandleFunc("GET /admin/api/projects/{project}/graph-stats", a.protectAPI("", a.apiProjectGraphStats))
	mux.HandleFunc("GET /admin/api/projects/{project}/dead-code", a.protectAPI("", a.apiProjectDeadCode))
	mux.HandleFunc("GET /admin/api/projects/{project}/sbom", a.protectAPI("", a.apiProjectSbom))

	// --- Legacy form auth (POST) for older HTML pages; SPA uses /admin/api/* ---
	// GET /admin/login is served by the SPA (client route), not the HTML form.
	mux.HandleFunc("POST /admin/login", a.loginSubmit)
	mux.HandleFunc("POST /admin/logout", a.protect("", a.logout))
	mux.HandleFunc("GET /admin/keys", a.protect("", a.keysList))
	mux.HandleFunc("POST /admin/keys", a.protect("", a.keysCreate))
	mux.HandleFunc("POST /admin/keys/revoke", a.protect("", a.keysRevoke))
	mux.HandleFunc("GET /admin/tokens", a.protect("", a.tokensList))
	mux.HandleFunc("POST /admin/tokens", a.protect("", a.tokensCreate))
	mux.HandleFunc("POST /admin/tokens/revoke", a.protect("", a.tokensRevoke))
	mux.HandleFunc("GET /admin/account", a.protect("", a.accountForm))
	mux.HandleFunc("POST /admin/account", a.protect("", a.accountChangePassword))
	mux.HandleFunc("GET /admin/users", a.protect("admin", a.usersList))
	mux.HandleFunc("POST /admin/users", a.protect("admin", a.usersCreate))
	mux.HandleFunc("POST /admin/users/disable", a.protect("admin", a.usersDisable))

	// --- React SPA (projects, search, cli guide) + static assets ---------------
	// More specific /admin/api and legacy routes above win; this catches the rest.
	spa := spaFileServer()
	mux.Handle("GET /admin/{$}", spa)
	mux.Handle("GET /admin/", spa)
	return securityHeaders(mux)
}

// securityHeaders wraps the admin/SPA handler with defence-in-depth response
// headers. The CSP matches the Vite build: scripts and styles are served from
// /admin/assets (self); React sets inline style attributes, so styles also need
// 'unsafe-inline'. frame-ancestors 'none' blocks clickjacking.
func securityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; " +
		"script-src 'self'; " +
		"style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data:; " +
		"font-src 'self'; " +
		"connect-src 'self'; " +
		"frame-ancestors 'none'; " +
		"base-uri 'self'; " +
		"form-action 'self'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// authCtx carries the resolved session for a request.
type authCtx struct {
	user    *store.User
	session string // plaintext session token (for CSRF derivation)
	csrf    string
}

type authedHandler func(http.ResponseWriter, *http.Request, *authCtx)

// protect resolves the session, enforces CSRF on unsafe methods, and checks the
// role before invoking fn. An unauthenticated GET redirects to the login page.
func (a *Admin) protect(role string, fn authedHandler) http.HandlerFunc {
	return a.protectMode(role, fn, false)
}

// protectAPI is like protect but returns JSON 401/403 instead of HTML redirects,
// for the React SPA.
func (a *Admin) protectAPI(role string, fn authedHandler) http.HandlerFunc {
	return a.protectMode(role, fn, true)
}

func (a *Admin) protectMode(role string, fn authedHandler, jsonAPI bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ac, ok := a.resolveAuthCtx(w, r, jsonAPI)
		if !ok {
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !a.validCSRF(r, ac) {
			a.writeCSRFError(w, jsonAPI)
			return
		}
		if role == "admin" && ac.user.Role != "admin" {
			a.writeRoleError(w, jsonAPI)
			return
		}
		fn(w, r, ac)
	}
}

// --- sessions & cookies ------------------------------------------------------

// sessionCookie creates an http.Cookie with security attributes. Secure follows
// New(..., secureCookies): true behind HTTPS (production), false for local HTTP
// demos and tests (SEMIDX_COOKIE_SECURE=false).
func (a *Admin) sessionCookie(name, value string, ttl time.Duration, maxAge int) *http.Cookie {
	// #nosec G124 -- Secure follows SEMIDX_COOKIE_SECURE (false only for deliberate plain-HTTP demos/tests).
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/admin",
		HttpOnly: true,
		Secure:   a.secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(ttl),
		MaxAge:   maxAge,
	}
}

func (a *Admin) setSession(w http.ResponseWriter, plaintext string, ttl time.Duration) {
	http.SetCookie(w, a.sessionCookie(sessionCookie, plaintext, ttl, 0))
}

func (a *Admin) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, a.sessionCookie(sessionCookie, "", 0, -1))
}

func (a *Admin) redirectLogin(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// newSessionToken returns a random session token and its storage hash.
func newSessionToken() (plaintext, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	plaintext = hex.EncodeToString(b)
	return plaintext, hashToken(plaintext), nil
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// generateAPIToken mints an API token in the same shape the server's Bearer auth
// expects: a "semidx_"-prefixed plaintext and its SHA-256 hash for storage.
func generateAPIToken() (plaintext, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	plaintext = "semidx_" + hex.EncodeToString(b)
	return plaintext, hashToken(plaintext), nil
}

// --- CSRF (synchronizer token bound to the session) --------------------------

func (a *Admin) csrfToken(sessionPlaintext string) string {
	mac := hmac.New(sha256.New, a.csrfKey)
	mac.Write([]byte(sessionPlaintext))
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *Admin) validCSRF(r *http.Request, ac *authCtx) bool {
	got := r.FormValue("csrf_token")
	if got == "" {
		got = r.Header.Get("X-CSRF-Token")
	}
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(ac.csrf)) == 1
}

// --- login rate limiter ------------------------------------------------------

type loginLimiter struct {
	mu    sync.Mutex
	tries map[string][]time.Time
}

// allowed reports whether key has attempts left in the current window.
func (l *loginLimiter) allowed(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tries[key] = prune(l.tries[key], now.Add(-loginWindow))
	return len(l.tries[key]) < loginMaxTries
}

func (l *loginLimiter) record(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tries[key] = append(prune(l.tries[key], now.Add(-loginWindow)), now)
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.tries, key)
}

// reap periodically removes keys whose last attempt is outside the login window.
func (l *loginLimiter) reap() {
	for {
		time.Sleep(5 * time.Minute)
		l.mu.Lock()
		now := time.Now()
		cutoff := now.Add(-loginWindow)
		for key, tries := range l.tries {
			allStale := true
			for _, t := range tries {
				if t.After(cutoff) {
					allStale = false
					break
				}
			}
			if allStale {
				delete(l.tries, key)
			}
		}
		l.mu.Unlock()
	}
}

func prune(ts []time.Time, cutoff time.Time) []time.Time {
	kept := ts[:0]
	for _, t := range ts {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	return kept
}

// discard is an io.Writer that drops everything (nil-logger sink).
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
