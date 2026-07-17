package jwtauth

import (
	"crypto"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TestVerifyRejectsNonHS256MethodInstance hits the keyfunc branch that rejects
// tokens whose Method is not the canonical jwt.SigningMethodHS256 pointer.
// We temporarily re-register "HS256" to return a distinct *SigningMethodHMAC
// instance so Parse still accepts alg=HS256 (WithValidMethods) but the pointer
// comparison in Verify fails.
//
// Must not use t.Parallel — mutates the process-global JWT method registry.
// coverage-patch: 2026-07-17
func TestVerifyRejectsNonHS256MethodInstance(t *testing.T) {
	// Restore the real HS256 factory after the test.
	t.Cleanup(func() {
		jwt.RegisterSigningMethod(jwt.SigningMethodHS256.Alg(), func() jwt.SigningMethod {
			return jwt.SigningMethodHS256
		})
	})

	// Distinct instance with the same alg name and hash.
	impostor := &jwt.SigningMethodHMAC{Name: "HS256", Hash: crypto.SHA256}
	jwt.RegisterSigningMethod("HS256", func() jwt.SigningMethod {
		return impostor
	})

	i := mustIssuer(t)
	// Sign with the real SigningMethodHS256 (Mint / NewWithClaims use the var).
	m, err := i.Mint("alice", []string{"read"}, time.Hour, time.Now())
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	// Verify parses alg=HS256 → GetSigningMethod returns impostor → keyfunc rejects.
	_, err = i.Verify(m.Token)
	if err == nil {
		t.Fatal("Verify should reject token when Method is not SigningMethodHS256")
	}
	if !strings.Contains(err.Error(), "unexpected signing method") {
		// error is wrapped by jwt as "error while executing keyfunc: ..."
		t.Logf("Verify error: %v", err)
	}
}

// TestMintSignedStringError forces HMAC Sign to fail by swapping the Hash on
// the global SigningMethodHS256 to an unavailable crypto.Hash.
//
// Must not use t.Parallel — mutates jwt.SigningMethodHS256.
// coverage-patch: 2026-07-17
func TestMintSignedStringError(t *testing.T) {
	orig := jwt.SigningMethodHS256.Hash
	t.Cleanup(func() { jwt.SigningMethodHS256.Hash = orig })

	// crypto.Hash(0) is not available → Sign returns ErrHashUnavailable.
	jwt.SigningMethodHS256.Hash = crypto.Hash(0)

	i := mustIssuer(t)
	_, err := i.Mint("alice", []string{"read"}, time.Hour, time.Now())
	if err == nil {
		t.Fatal("Mint should fail when HS256 hash is unavailable")
	}
}
