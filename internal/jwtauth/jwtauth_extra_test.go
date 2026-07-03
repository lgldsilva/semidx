package jwtauth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"pgregory.net/rapid"
)

// TestMintVerifyPreservesClaims is a property check: for any subject and any set
// of scopes, a minted token verifies and round-trips the subject and scopes
// exactly.
func TestMintVerifyPreservesClaims(t *testing.T) {
	i := mustIssuer(t)
	rapid.Check(t, func(rt *rapid.T) {
		subject := rapid.String().Draw(rt, "subject")
		scopes := rapid.SliceOfN(
			rapid.SampledFrom([]string{"read", "write", "admin", "search", "index"}),
			0, 5,
		).Draw(rt, "scopes")

		m, err := i.Mint(subject, scopes, 0, time.Now())
		if err != nil {
			rt.Fatalf("Mint error: %v", err)
		}
		claims, err := i.Verify(m.Token)
		if err != nil {
			rt.Fatalf("Verify of a freshly minted token failed: %v", err)
		}
		if claims.Subject != subject {
			rt.Fatalf("subject = %q, want %q", claims.Subject, subject)
		}
		if claims.ID != m.JTI {
			rt.Fatalf("jti = %q, want %q", claims.ID, m.JTI)
		}
		if len(claims.Scopes) != len(scopes) {
			rt.Fatalf("scopes = %v, want %v", claims.Scopes, scopes)
		}
		for k := range scopes {
			if claims.Scopes[k] != scopes[k] {
				rt.Fatalf("scope[%d] = %q, want %q", k, claims.Scopes[k], scopes[k])
			}
		}
	})
}

// TestVerifyRejectsTamperedToken is a property check: flipping any character in
// the header/payload/signature of a valid token must make Verify fail (the MAC
// is computed over the exact header.payload text and compared to the signature).
func TestVerifyRejectsTamperedToken(t *testing.T) {
	i := mustIssuer(t)
	rapid.Check(t, func(rt *rapid.T) {
		m, err := i.Mint("alice", []string{"read"}, time.Hour, time.Now())
		if err != nil {
			rt.Fatalf("Mint error: %v", err)
		}
		tok := m.Token
		// Stay clear of the final characters of the signature segment, whose
		// unused base64 bits could decode to the same MAC bytes.
		idx := rapid.IntRange(0, len(tok)-6).Draw(rt, "index")
		repl := byte('A')
		if tok[idx] == 'A' {
			repl = 'B'
		}
		tampered := tok[:idx] + string(repl) + tok[idx+1:]
		if tampered == tok {
			rt.Fatal("tamper produced an identical token")
		}
		if _, err := i.Verify(tampered); err == nil {
			rt.Fatalf("Verify accepted a tampered token (flipped index %d)", idx)
		}
	})
}

// TestVerifyRejectsMissingJTI covers the branch where a token is otherwise valid
// (right issuer, right signature, HS256) but carries no jti.
func TestVerifyRejectsMissingJTI(t *testing.T) {
	i := mustIssuer(t)
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:  issuer,
			Subject: "alice",
			// ID (jti) deliberately left empty.
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(i.secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := i.Verify(signed); err == nil {
		t.Error("a token without a jti must be rejected")
	}
}
