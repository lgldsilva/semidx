package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"slices"
	"strings"

	"github.com/lgldsilva/semidx/internal/passwd"
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
		scopes, ok, err := s.resolveScopes(r.Context(), raw)
		if err != nil {
			s.log.Error("token lookup failed", "err", err)
			writeJSONError(w, http.StatusInternalServerError, "auth check failed")
			return
		}
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		if scope != "" && !slices.Contains(scopes, scope) && !slices.Contains(scopes, "admin") {
			writeJSONError(w, http.StatusForbidden, "token missing required scope: "+scope)
			return
		}
		// Expose the token's scopes downstream so multiplexed handlers (the
		// /mcp endpoint routes several tools through one HTTP route) can apply
		// finer-grained checks than the route-level scope.
		h(w, r.WithContext(contextWithScopes(r.Context(), scopes)))
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
	if s.jwt != nil {
		if claims, verr := s.jwt.Verify(raw); verr == nil {
			tok, terr := s.store.TokenByHash(ctx, claims.ID) // jti stored in token_hash
			if terr != nil {
				return nil, false, terr
			}
			if tok == nil {
				return nil, false, nil // revoked or unknown jti
			}
			return claims.Scopes, true, nil
		}
	}
	tok, terr := s.store.TokenByHash(ctx, HashToken(raw))
	if terr != nil {
		return nil, false, terr
	}
	if tok == nil {
		return nil, false, nil
	}
	return tok.Scopes, true, nil
}

func bearerToken(r *http.Request) string {
	if after, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
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
