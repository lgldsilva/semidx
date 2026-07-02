package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"slices"
	"strings"
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
// scope (or "admin", which grants everything).
func (s *Server) authed(scope string, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r)
		if raw == "" {
			writeJSONError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		tok, err := s.store.TokenByHash(r.Context(), HashToken(raw))
		if err != nil {
			s.log.Error("token lookup failed", "err", err)
			writeJSONError(w, http.StatusInternalServerError, "auth check failed")
			return
		}
		if tok == nil {
			writeJSONError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		if scope != "" && !slices.Contains(tok.Scopes, scope) && !slices.Contains(tok.Scopes, "admin") {
			writeJSONError(w, http.StatusForbidden, "token missing required scope: "+scope)
			return
		}
		h(w, r)
	})
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
