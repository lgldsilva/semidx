package jwtauth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestVerifyRejectsMissingJTI(t *testing.T) {
	t.Parallel()
	i := mustIssuer(t)

	// Create a token with the correct secret but missing jti.
	claims := &Claims{
		Scopes: []string{"read"},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   "test",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			// ID is empty — no jti
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("test-secret-please-change"))
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}

	if _, err := i.Verify(token); err == nil {
		t.Error("Verify should reject token with empty jti")
	} else if !strings.Contains(err.Error(), "missing jti") {
		t.Errorf("Verify error = %q, want 'missing jti'", err.Error())
	}
}

// TestMintWithEmptyScopes verifies Mint works with nil scopes.
func TestMintWithEmptyScopes(t *testing.T) {
	t.Parallel()
	i := mustIssuer(t)
	now := time.Now()

	m, err := i.Mint("svc", nil, time.Hour, now)
	if err != nil {
		t.Fatalf("Mint(nil scopes): %v", err)
	}
	if m.JTI == "" {
		t.Error("Mint(nil) produced empty jti")
	}

	claims, err := i.Verify(m.Token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Scopes != nil {
		t.Logf("Scopes: %v", claims.Scopes)
	}
}

// TestMintNonExpiring verifies the non-expiring path (ttl=0) explicitly
// with the ExpiresAt check.
func TestMintNonExpiring(t *testing.T) {
	t.Parallel()
	i := mustIssuer(t)

	m, err := i.Mint("daemon", []string{"admin"}, 0, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Mint(ttl=0): %v", err)
	}
	if m.ExpiresAt != nil {
		t.Errorf("non-expiring token has ExpiresAt = %v, want nil", m.ExpiresAt)
	}

	// Verify must still pass.
	if _, err := i.Verify(m.Token); err != nil {
		t.Errorf("non-expiring token verification failed: %v", err)
	}
}

// TestVerifyRejectsTokenFromDifferentIssuer ensures issuer validation works.
func TestVerifyRejectsTokenFromDifferentIssuer(t *testing.T) {
	t.Parallel()
	i := mustIssuer(t)

	// Token with the right secret but wrong issuer.
	claims := &Claims{
		Scopes: []string{"read"},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:  "evil",
			Subject: "test",
			ID:      "some-jti",
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("test-secret-please-change"))
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}

	if _, err := i.Verify(token); err == nil {
		t.Error("Verify should reject token with wrong issuer")
	}
}
