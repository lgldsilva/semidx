package webadmin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lgldsilva/semidx/internal/store"
)

// tokensData is the control-tokens page payload.
type tokensData struct {
	Enabled  bool
	Tokens   []store.Token
	NewToken string // plaintext JWT of a just-minted token, shown once
}

func (a *Admin) tokensList(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	a.renderTokens(w, r, ac, "", "", "")
}

func (a *Admin) renderTokens(w http.ResponseWriter, r *http.Request, ac *authCtx, newToken, flash, errMsg string) {
	d := tokensData{Enabled: a.jwt != nil, NewToken: newToken}
	if a.jwt != nil {
		tokens, err := a.store.ListUserTokens(r.Context(), ac.user.ID, "jwt")
		if err != nil {
			a.log.Error("list control tokens failed", "err", err)
			errMsg = "could not load control tokens"
		}
		d.Tokens = tokens
	}
	a.render(w, "tokens.html", page{
		User: ac.user, CSRF: ac.csrf, Active: "tokens", Flash: flash, Err: errMsg, Data: d,
	})
}

func (a *Admin) tokensCreate(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	if a.jwt == nil {
		http.Error(w, "control tokens are disabled (set SEMIDX_JWT_SECRET)", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		a.renderTokens(w, r, ac, "", "", "a token name is required")
		return
	}
	scopes, err := scopesFromForm(r.Form["scopes"], ac.user.Role)
	if err != nil {
		a.renderTokens(w, r, ac, "", "", err.Error())
		return
	}

	ttl := parseTTLDays(r.FormValue("ttl_days")) // 0 = never expires
	minted, err := a.jwt.Mint(ac.user.Username, scopes, ttl, time.Now())
	if err != nil {
		a.log.Error("mint token failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if _, err := a.store.CreateJWTToken(r.Context(), ac.user.ID, name, minted.JTI, scopes, minted.ExpiresAt); err != nil {
		a.log.Error("record control token failed", "err", err)
		a.renderTokens(w, r, ac, "", "", "could not create token")
		return
	}
	a.renderTokens(w, r, ac, minted.Token, "Control token created — copy it now, it won't be shown again.", "")
}

func (a *Admin) tokensRevoke(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	id, err := strconv.Atoi(r.FormValue("id"))
	if err != nil {
		a.renderTokens(w, r, ac, "", "", "invalid token id")
		return
	}
	switch err := a.store.RevokeUserToken(r.Context(), ac.user.ID, id); {
	case errors.Is(err, store.ErrNotFound):
		a.renderTokens(w, r, ac, "", "", "token not found")
	case err != nil:
		a.log.Error("revoke control token failed", "err", err)
		a.renderTokens(w, r, ac, "", "", "could not revoke token")
	default:
		a.renderTokens(w, r, ac, "", "Control token revoked.", "")
	}
}

// parseTTLDays converts a days string to a duration; empty/0/invalid means no
// expiration.
func parseTTLDays(s string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * 24 * time.Hour
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
