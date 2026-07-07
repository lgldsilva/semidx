// Package webadmin is the server's management UI, mounted at /admin by
// `semidx serve`. It is server-rendered (html/template, no external JS) and
// embedded in the binary. Auth is a cookie-backed server-side session; every
// mutating request carries a CSRF token bound to the session; roles gate access.
package webadmin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"sync"
	"time"

	embedpkg "github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/jwtauth"
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

// Admin renders and serves the management UI.
type Admin struct {
	store   store.Store
	search  *search.Service
	tmpl    *template.Template
	log     *slog.Logger
	secure  bool // set the Secure flag on cookies (serve behind HTTPS)
	csrfKey []byte
	limiter *loginLimiter
	jwt     *jwtauth.Issuer // nil when control tokens are disabled
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
		search:  search.NewService(st, emb),
		tmpl:    tmpl,
		log:     log,
		secure:  secureCookies,
		csrfKey: key,
		limiter: limiter,
		jwt:     jwt,
	}, nil
}

// Handler returns a mux serving the /admin/* routes.
func (a *Admin) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/login", a.loginForm)
	mux.HandleFunc("POST /admin/login", a.loginSubmit)
	mux.HandleFunc("POST /admin/logout", a.protect("", a.logout))
	mux.HandleFunc("GET /admin/{$}", a.protect("", a.dashboard))
	mux.HandleFunc("GET /admin/api/projects", a.protect("", a.projectsAPI))
	mux.HandleFunc("GET /admin/search", a.protect("", a.searchPage))
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
	return mux
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
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			a.redirectLogin(w, r)
			return
		}
		user, err := a.store.SessionUser(r.Context(), hashToken(cookie.Value))
		if errors.Is(err, store.ErrNotFound) {
			a.clearSession(w)
			a.redirectLogin(w, r)
			return
		}
		if err != nil {
			a.log.Error("session lookup failed", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		ac := &authCtx{user: user, session: cookie.Value, csrf: a.csrfToken(cookie.Value)}

		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !a.validCSRF(r, ac) {
				http.Error(w, "invalid or missing CSRF token", http.StatusForbidden)
				return
			}
		}
		if role == "admin" && user.Role != "admin" {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		fn(w, r, ac)
	}
}

// --- sessions & cookies ------------------------------------------------------

// sessionCookie creates an http.Cookie with security attributes. The Secure
// flag is always true (admin is designed to be served behind HTTPS); callers
// that serve over plain HTTP (e.g. tests) must provide a client that handles
// Secure cookies or use an HTTPS test server.
func (a *Admin) sessionCookie(name, value string, ttl time.Duration, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/admin",
		HttpOnly: true,
		Secure:   true,
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
