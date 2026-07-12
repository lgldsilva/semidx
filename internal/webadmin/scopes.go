package webadmin

import (
	"errors"
	"fmt"
)

var validScopes = map[string]bool{
	"read":  true,
	"write": true,
	"admin": true,
}

// normalizeScopes returns the form scopes or ["read"] when empty.
func normalizeScopes(form []string) []string {
	if len(form) == 0 {
		return []string{"read"}
	}
	return form
}

// validateScopes checks that every scope is known and that non-admins cannot
// request admin scope. Returns a user-facing error message when invalid.
func validateScopes(scopes []string, role string) error {
	for _, s := range scopes {
		if !validScopes[s] {
			return fmt.Errorf("invalid scope: %s (allowed: read, write, admin)", s)
		}
	}
	if contains(scopes, "admin") && role != "admin" {
		return errors.New("only admins can issue admin-scoped tokens")
	}
	return nil
}

// scopesFromForm reads and validates scopes from an admin form submission.
func scopesFromForm(form []string, role string) ([]string, error) {
	scopes := normalizeScopes(form)
	if err := validateScopes(scopes, role); err != nil {
		return nil, err
	}
	return scopes, nil
}
