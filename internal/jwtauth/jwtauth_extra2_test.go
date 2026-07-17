package jwtauth

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// TestVerifyRejectsWrongSigningMethod verifies tokens signed with
// HS384 (not the expected HS256) are rejected because jwt.WithValidMethods
// catches the mismatch before the key function is called.
// coverage-patch: 2026-07-17
func TestVerifyRejectsWrongSigningMethod(t *testing.T) {
	t.Parallel()
	i := mustIssuer(t)

	// Create a token signed with HS384 instead of HS256.
	claims := &Claims{
		Scopes: []string{"read"},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:  issuer,
			Subject: "test",
			ID:      "some-jti",
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS384, claims).
		SignedString([]byte("test-secret-please-change"))
	if err != nil {
		t.Fatalf("sign test token with HS384: %v", err)
	}

	if _, err := i.Verify(token); err == nil {
		t.Fatal("Verify should reject HS384-signed token")
	}
	// jwt.WithValidMethods produces an error mentioning the invalid method.
	// The exact wording varies by library version — we just check it fails.
}
