// Package auth: identity layer that examines incoming credentials and
// decides whether to grant access to protected routes.
package auth

import (
	"graphrag-fixture/jwt"
	"graphrag-fixture/log"
)

// CheckCredential inspects the supplied identity proof and delegates
// the cryptographic processing to the downstream module.
func CheckCredential(proof string) error {
	log.Debug("auth: examining identity proof")
	if proof == "" {
		return ErrEmptyProof
	}
	return jwt.DecodeAndCompare(proof)
}

var ErrEmptyProof = identityError("empty identity proof")

type identityError string

func (e identityError) Error() string { return string(e) }
