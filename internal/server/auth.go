package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/lgldsilva/semidx/internal/passwd"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/internal/tenant"
)

// GenerateToken returns a new random API token (plaintext, shown once) and its
// SHA-256 hash for storage.
func GenerateToken() (plaintext, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	plaintext = "semidx_" + hex.EncodeToString(b)
	return plaintext, HashToken(plaintext), nil
}

// HashToken returns the hex SHA-256 of a token (what the DB stores).
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// authed wraps a handler so it requires a valid Bearer token carrying the given
// scope (or "admin", which grants everything). The bearer may be an opaque API
// key or a JWT control token; both resolve to a set of scopes.
func (s *Server) authed(scope string, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r)
		if raw == "" {
			writeJSONError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		identity, ok, err := s.resolveAuth(r.Context(), raw)
		if err != nil {
			s.log.Error("token lookup failed", "err", err)
			writeJSONError(w, http.StatusInternalServerError, "auth check failed")
			return
		}
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		if scope != "" && !slices.Contains(identity.Scopes, scope) && !slices.Contains(identity.Scopes, "admin") {
			writeJSONError(w, http.StatusForbidden, "token missing required scope: "+scope)
			return
		}
		tenantID := identity.TenantID
		tenantSlug := ""
		if requested := strings.TrimSpace(r.Header.Get("X-Semidx-Tenant")); requested != "" {
			ts, supported := s.store.(store.TenantStore)
			if !supported {
				writeJSONError(w, http.StatusNotImplemented, "tenant selection requires PostgreSQL")
				return
			}
			t, terr := ts.GetTenantBySlug(r.Context(), requested)
			if errors.Is(terr, store.ErrNotFound) {
				writeJSONError(w, http.StatusForbidden, "tenant access denied")
				return
			}
			if terr != nil {
				s.log.Error("tenant lookup failed", "err", terr)
				writeJSONError(w, http.StatusInternalServerError, "tenant lookup failed")
				return
			}
			allowed := t.ID == tenantID
			if !allowed && identity.UserID > 0 {
				allowed, terr = ts.CanAccessTenant(r.Context(), identity.UserID, t.ID)
				if terr != nil {
					s.log.Error("tenant membership lookup failed", "err", terr)
					writeJSONError(w, http.StatusInternalServerError, "tenant access check failed")
					return
				}
			}
			if !allowed {
				writeJSONError(w, http.StatusForbidden, "tenant access denied")
				return
			}
			tenantID, tenantSlug = t.ID, t.Slug
		}
		baseCtx, err := tenant.With(r.Context(), tenant.Context{
			ID: tenantID, Slug: tenantSlug, UserID: identity.UserID,
		})
		if err != nil {
			s.log.Error("token tenant context failed", "err", err)
			writeJSONError(w, http.StatusInternalServerError, "auth context failed")
			return
		}
		workspaceID := 0
		workspaceSlug := ""
		if ws, supported := s.store.(store.WorkspaceStore); supported {
			requestedWorkspace := strings.TrimSpace(r.Header.Get("X-Semidx-Workspace"))
			if requestedWorkspace == "" {
				requestedWorkspace = "default"
			}
			workspace, werr := ws.GetWorkspaceBySlug(baseCtx, requestedWorkspace)
			if errors.Is(werr, store.ErrNotFound) {
				writeJSONError(w, http.StatusForbidden, "workspace access denied")
				return
			}
			if werr != nil {
				s.log.Error("workspace lookup failed", "err", werr)
				writeJSONError(w, http.StatusInternalServerError, "workspace lookup failed")
				return
			}
			workspaceID, workspaceSlug = workspace.ID, workspace.Slug
		}
		ctx, err := tenant.With(baseCtx, tenant.Context{
			ID: tenantID, Slug: tenantSlug, WorkspaceID: workspaceID, Workspace: workspaceSlug, UserID: identity.UserID,
		})
		if err != nil {
			s.log.Error("token tenant context failed", "err", err)
			writeJSONError(w, http.StatusInternalServerError, "auth context failed")
			return
		}
		// Expose the token's scopes downstream so multiplexed handlers (the
		// /mcp endpoint routes several tools through one HTTP route) can apply
		// finer-grained checks than the route-level scope.
		h(w, r.WithContext(contextWithScopes(ctx, identity.Scopes)))
	})
}

// scopesKey is the context key under which authed stores the bearer's scopes.
type scopesKey struct{}

// contextWithScopes returns ctx carrying the authenticated token's scopes.
func contextWithScopes(ctx context.Context, scopes []string) context.Context {
	return context.WithValue(ctx, scopesKey{}, scopes)
}

// ScopesFromContext returns the scopes authed stored for this request, or nil
// when the context did not pass through authed.
func ScopesFromContext(ctx context.Context) []string {
	scopes, _ := ctx.Value(scopesKey{}).([]string)
	return scopes
}

// hasScope reports whether the context's token carries the scope (or "admin",
// which grants everything) — the same rule authed applies at the route level.
func hasScope(ctx context.Context, scope string) bool {
	scopes := ScopesFromContext(ctx)
	return slices.Contains(scopes, scope) || slices.Contains(scopes, "admin")
}

// resolveScopes authenticates a bearer and returns its scopes. A JWT control
// token is verified (signature, alg, expiry) and then checked for revocation by
// its jti; an opaque key is looked up by hash. ok=false means invalid or revoked;
// a non-nil error signals a backend failure (surface 500, not 401).
func (s *Server) resolveScopes(ctx context.Context, raw string) (scopes []string, ok bool, err error) {
	identity, ok, err := s.resolveAuth(ctx, raw)
	return identity.Scopes, ok, err
}

type authIdentity struct {
	Scopes   []string
	TenantID int
	UserID   int
}

func (s *Server) resolveAuth(ctx context.Context, raw string) (identity authIdentity, ok bool, err error) {
	if s.jwt != nil {
		if claims, verr := s.jwt.Verify(raw); verr == nil {
			tok, terr := s.store.TokenByHash(ctx, claims.ID) // jti stored in token_hash
			if terr != nil {
				return authIdentity{}, false, terr
			}
			if tok == nil {
				return authIdentity{}, false, nil // revoked or unknown jti
			}
			return authIdentity{Scopes: claims.Scopes, TenantID: normalizedTenantID(tok.TenantID), UserID: tok.UserID}, true, nil
		}
	}
	tok, terr := s.store.TokenByHash(ctx, HashToken(raw))
	if terr != nil {
		return authIdentity{}, false, terr
	}
	if tok == nil {
		return authIdentity{}, false, nil
	}
	return authIdentity{Scopes: tok.Scopes, TenantID: normalizedTenantID(tok.TenantID), UserID: tok.UserID}, true, nil
}

func normalizedTenantID(id int) int {
	if id <= 0 {
		return tenant.DefaultID
	}
	return id
}

func bearerToken(r *http.Request) string {
	if after, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

// bearerHasAdminScope reports whether the request bearer carries the admin scope.
func (s *Server) bearerHasAdminScope(r *http.Request) (bool, error) {
	raw := bearerToken(r)
	if raw == "" {
		return false, nil
	}
	scopes, ok, err := s.resolveScopes(r.Context(), raw)
	if err != nil || !ok {
		return false, err
	}
	return slices.Contains(scopes, "admin"), nil
}

// EnsureBootstrapToken creates the first admin token on an empty server. If
// envToken is set it becomes that token; otherwise a random one is generated and
// returned so the caller can show it once. Returns "" when tokens already exist.
func (s *Server) EnsureBootstrapToken(ctx context.Context, envToken string) (string, error) {
	n, err := s.store.CountTokens(ctx)
	if err != nil {
		return "", err
	}
	if n > 0 {
		return "", nil
	}

	plaintext := envToken
	if plaintext == "" {
		plaintext, _, err = GenerateToken()
		if err != nil {
			return "", err
		}
	}
	if _, err := s.store.CreateToken(ctx, "bootstrap-admin", HashToken(plaintext), []string{"admin"}); err != nil {
		return "", err
	}
	return plaintext, nil
}

// EnsureBootstrapAdmin creates the first web-UI admin on a server that has no
// users yet, using the configured password. It returns the created username, or
// "" when skipped (no password set, or users already exist).
func (s *Server) EnsureBootstrapAdmin(ctx context.Context, username, password string) (string, error) {
	if password == "" {
		return "", nil
	}
	n, err := s.store.CountUsers(ctx)
	if err != nil {
		return "", err
	}
	if n > 0 {
		return "", nil
	}
	hash, err := passwd.Hash(password)
	if err != nil {
		return "", err
	}
	if _, err := s.store.CreateUser(ctx, username, hash, "admin"); err != nil {
		return "", err
	}
	return username, nil
}
