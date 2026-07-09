package webadmin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lgldsilva/semidx/internal/passwd"
	"github.com/lgldsilva/semidx/internal/store"
)

type tokenJSON struct {
	ID         int        `json:"id"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	Kind       string     `json:"kind"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

func tokensToJSON(tokens []store.Token) []tokenJSON {
	out := make([]tokenJSON, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, tokenJSON{
			ID: t.ID, Name: t.Name, Scopes: t.Scopes, Kind: t.Kind,
			CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt,
		})
	}
	return out
}

func (a *Admin) apiListKeys(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	tokens, err := a.store.ListUserTokens(r.Context(), ac.user.ID, "opaque")
	if err != nil {
		a.log.Error("list keys failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not load keys")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": tokensToJSON(tokens)})
}

type createKeyBody struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

func (a *Admin) apiCreateKey(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	var body createKeyBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeJSONErr(w, http.StatusBadRequest, "name is required")
		return
	}
	scopes, err := parseJSONScopes(body.Scopes, ac.user.Role)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	plaintext, hash, err := generateAPIToken()
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	id, err := a.store.CreateUserToken(r.Context(), ac.user.ID, name, hash, scopes)
	if err != nil {
		a.log.Error("create key failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not create key")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": id, "name": name, "scopes": scopes, "token": plaintext,
		"message": "copy the token now — it will not be shown again",
	})
}

func (a *Admin) apiRevokeKey(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid key id")
		return
	}
	switch err := a.store.RevokeUserToken(r.Context(), ac.user.ID, id); {
	case errors.Is(err, store.ErrNotFound):
		writeJSONErr(w, http.StatusNotFound, "key not found")
	case err != nil:
		a.log.Error("revoke key failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not revoke key")
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func (a *Admin) apiListTokens(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	if a.jwt == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "tokens": []any{}})
		return
	}
	tokens, err := a.store.ListUserTokens(r.Context(), ac.user.ID, "jwt")
	if err != nil {
		a.log.Error("list control tokens failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not load tokens")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "tokens": tokensToJSON(tokens)})
}

type createTokenBody struct {
	Name    string   `json:"name"`
	Scopes  []string `json:"scopes"`
	TTLDays int      `json:"ttl_days"`
}

func (a *Admin) apiCreateToken(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	if a.jwt == nil {
		writeJSONErr(w, http.StatusForbidden, "control tokens are disabled (set SEMIDX_JWT_SECRET)")
		return
	}
	var body createTokenBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeJSONErr(w, http.StatusBadRequest, "name is required")
		return
	}
	scopes, err := parseJSONScopes(body.Scopes, ac.user.Role)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ttl := time.Duration(0)
	if body.TTLDays > 0 {
		ttl = time.Duration(body.TTLDays) * 24 * time.Hour
	}
	minted, err := a.jwt.Mint(ac.user.Username, scopes, ttl, time.Now())
	if err != nil {
		a.log.Error("mint token failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	id, err := a.store.CreateJWTToken(r.Context(), ac.user.ID, name, minted.JTI, scopes, minted.ExpiresAt)
	if err != nil {
		a.log.Error("record control token failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not create token")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": id, "name": name, "scopes": scopes, "token": minted.Token,
		"expires_at": minted.ExpiresAt,
		"message":    "copy the token now — it will not be shown again",
	})
}

func (a *Admin) apiRevokeToken(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid token id")
		return
	}
	switch err := a.store.RevokeUserToken(r.Context(), ac.user.ID, id); {
	case errors.Is(err, store.ErrNotFound):
		writeJSONErr(w, http.StatusNotFound, "token not found")
	case err != nil:
		a.log.Error("revoke token failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not revoke token")
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func (a *Admin) apiChangePassword(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if ok, _ := passwd.Verify(body.Current, ac.user.PasswordHash); !ok {
		writeJSONErr(w, http.StatusBadRequest, "current password is incorrect")
		return
	}
	if len(body.New) < 8 {
		writeJSONErr(w, http.StatusBadRequest, "new password must be at least 8 characters")
		return
	}
	hash, err := passwd.Hash(body.New)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	if err := a.store.SetUserPassword(r.Context(), ac.user.ID, hash); err != nil {
		a.log.Error("set password failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not update password")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *Admin) apiListUsers(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	limit, offset := parseListParams(r)
	users, err := a.store.ListUsers(r.Context(), limit, offset)
	if err != nil {
		a.log.Error("list users failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not load users")
		return
	}
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, map[string]any{
			"id": u.ID, "username": u.Username, "role": u.Role, "disabled": u.Disabled,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (a *Admin) apiCreateUser(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	role := body.Role
	if role != "admin" {
		role = "member"
	}
	username := strings.TrimSpace(body.Username)
	if username == "" || len(body.Password) < 8 {
		writeJSONErr(w, http.StatusBadRequest, "username required and password must be at least 8 characters")
		return
	}
	hash, err := passwd.Hash(body.Password)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	switch _, err := a.store.CreateUser(r.Context(), username, hash, role); {
	case errors.Is(err, store.ErrUserExists):
		writeJSONErr(w, http.StatusConflict, "a user with that name already exists")
	case err != nil:
		a.log.Error("create user failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not create user")
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "username": username, "role": role})
	}
}

func (a *Admin) apiSetUserDisabled(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if id == ac.user.ID {
		writeJSONErr(w, http.StatusBadRequest, "you cannot disable your own account")
		return
	}
	var body struct {
		Disabled bool `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	switch err := a.store.SetUserDisabled(r.Context(), id, body.Disabled); {
	case errors.Is(err, store.ErrNotFound):
		writeJSONErr(w, http.StatusNotFound, "user not found")
	case err != nil:
		a.log.Error("set user disabled failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not update user")
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// parseJSONScopes mirrors scopesFromForm for JSON arrays.
func parseJSONScopes(scopes []string, role string) ([]string, error) {
	return scopesFromForm(scopes, role)
}
