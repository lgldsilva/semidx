package webadmin

import (
	"errors"
	"net/http"

	"github.com/lgldsilva/semidx/internal/store"
)

// resolveAuthCtx loads the session user and CSRF token, or writes the appropriate
// auth failure response and returns false.
func (a *Admin) resolveAuthCtx(w http.ResponseWriter, r *http.Request, jsonAPI bool) (*authCtx, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		a.writeUnauthorized(w, r, jsonAPI)
		return nil, false
	}
	user, err := a.store.SessionUser(r.Context(), hashToken(cookie.Value))
	if errors.Is(err, store.ErrNotFound) {
		a.clearSession(w)
		a.writeUnauthorized(w, r, jsonAPI)
		return nil, false
	}
	if err != nil {
		a.log.Error("session lookup failed", "err", err)
		a.writeInternalError(w, jsonAPI)
		return nil, false
	}
	return &authCtx{user: user, session: cookie.Value, csrf: a.csrfToken(cookie.Value)}, true
}

func (a *Admin) writeUnauthorized(w http.ResponseWriter, r *http.Request, jsonAPI bool) {
	if jsonAPI {
		writeJSONErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	a.redirectLogin(w, r)
}

func (a *Admin) writeInternalError(w http.ResponseWriter, jsonAPI bool) {
	if jsonAPI {
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func (a *Admin) writeCSRFError(w http.ResponseWriter, jsonAPI bool) {
	if jsonAPI {
		writeJSONErr(w, http.StatusForbidden, "invalid or missing CSRF token")
		return
	}
	http.Error(w, "invalid or missing CSRF token", http.StatusForbidden)
}

func (a *Admin) writeRoleError(w http.ResponseWriter, jsonAPI bool) {
	if jsonAPI {
		writeJSONErr(w, http.StatusForbidden, "admin only")
		return
	}
	http.Error(w, "admin only", http.StatusForbidden)
}
