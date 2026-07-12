package jwtauth

import (
	"strings"
	"testing"
	"time"
)

func mustIssuer(t *testing.T) *Issuer {
	t.Helper()
	i, err := New("test-secret-please-change")
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func TestNewRejectsEmptySecret(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Error("New(\"\") should error")
	}
}

func TestMintVerifyRoundTrip(t *testing.T) {
	i := mustIssuer(t)
	now := time.Now()
	m, err := i.Mint("alice", []string{"read", "write"}, time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if m.JTI == "" || m.ExpiresAt == nil {
		t.Fatalf("minted = %+v; want jti and exp set", m)
	}
	claims, err := i.Verify(m.Token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "alice" || len(claims.Scopes) != 2 || claims.ID != m.JTI {
		t.Errorf("claims = %+v", claims)
	}
}

func TestMintNoExpiration(t *testing.T) {
	i := mustIssuer(t)
	m, err := i.Mint("svc", []string{"admin"}, 0, time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if m.ExpiresAt != nil {
		t.Errorf("ttl=0 should mean no expiry, got %v", m.ExpiresAt)
	}
	// A non-expiring token still verifies long "after" issuance.
	if _, err := i.Verify(m.Token); err != nil {
		t.Errorf("non-expiring token failed to verify: %v", err)
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	i := mustIssuer(t)
	past := time.Now().Add(-2 * time.Hour)
	m, _ := i.Mint("alice", []string{"read"}, time.Hour, past) // expired an hour ago
	if _, err := i.Verify(m.Token); err == nil {
		t.Error("expired token should not verify")
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	i := mustIssuer(t)
	m, _ := i.Mint("alice", []string{"read"}, time.Hour, time.Now())
	other, _ := New("a-different-secret")
	if _, err := other.Verify(m.Token); err == nil {
		t.Error("token signed with a different secret should not verify")
	}
}

func TestVerifyRejectsAlgNone(t *testing.T) {
	i := mustIssuer(t)
	// A token with alg=none must be rejected (alg-confusion guard).
	none := "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0." +
		"eyJpc3MiOiJzZW1pZHgiLCJzdWIiOiJhbGljZSIsImp0aSI6IngifQ."
	if _, err := i.Verify(none); err == nil {
		t.Error("alg=none token must be rejected")
	}
}

func TestVerifyRejectsGarbage(t *testing.T) {
	i := mustIssuer(t)
	for _, bad := range []string{"", "not.a.jwt", strings.Repeat("x", 40)} {
		if _, err := i.Verify(bad); err == nil {
			t.Errorf("Verify(%q) should error", bad)
		}
	}
}
