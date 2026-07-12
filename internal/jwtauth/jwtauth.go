// Package jwtauth mints and verifies semidx "control tokens": HS256 JWTs that
// carry a user's identity and roles (scopes). Tokens are signed with a shared
// secret; a token may be non-expiring. Each token carries a unique jti so it can
// be revoked out-of-band even when it never expires — the server records the jti
// and checks it on every request.
package jwtauth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const issuer = "semidx"

// Claims are the semidx-specific JWT payload.
type Claims struct {
	Scopes []string `json:"scopes"`
	jwt.RegisteredClaims
}

// Issuer signs and verifies control tokens with a fixed secret.
type Issuer struct {
	secret []byte
}

// New returns an Issuer, or an error if the secret is empty.
func New(secret string) (*Issuer, error) {
	if secret == "" {
		return nil, errors.New("jwtauth: empty secret")
	}
	return &Issuer{secret: []byte(secret)}, nil
}

// Minted is the result of issuing a token.
type Minted struct {
	Token     string     // the signed JWT (shown once)
	JTI       string     // unique id, recorded for revocation
	ExpiresAt *time.Time // nil when the token never expires
}

// Mint issues a control token for subject with the given scopes. A ttl of 0 means
// no expiration. now is passed in so callers (and tests) control the clock.
func (i *Issuer) Mint(subject string, scopes []string, ttl time.Duration, now time.Time) (Minted, error) {
	jti, err := randomID()
	if err != nil {
		return Minted{}, err
	}
	claims := Claims{
		Scopes: scopes,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:   issuer,
			Subject:  subject,
			ID:       jti,
			IssuedAt: jwt.NewNumericDate(now),
		},
	}
	var expPtr *time.Time
	if ttl > 0 {
		exp := now.Add(ttl)
		claims.ExpiresAt = jwt.NewNumericDate(exp)
		expPtr = &exp
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(i.secret)
	if err != nil {
		return Minted{}, err
	}
	return Minted{Token: signed, JTI: jti, ExpiresAt: expPtr}, nil
}

// Verify checks a token's signature, algorithm and expiry, returning its claims.
// It does NOT check revocation — the caller looks up the jti for that.
func (i *Issuer) Verify(token string) (*Claims, error) {
	var claims Claims
	_, err := jwt.ParseWithClaims(token, &claims, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return i.secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(issuer))
	if err != nil {
		return nil, err
	}
	if claims.ID == "" {
		return nil, errors.New("jwtauth: token missing jti")
	}
	return &claims, nil
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
