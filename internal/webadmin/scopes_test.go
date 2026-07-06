package webadmin

import "testing"

func TestNormalizeScopesEmpty(t *testing.T) {
	got := normalizeScopes(nil)
	if len(got) != 1 || got[0] != "read" {
		t.Fatalf("normalizeScopes(nil) = %v, want [read]", got)
	}
}

func TestValidateScopes(t *testing.T) {
	if err := validateScopes([]string{"read", "write"}, "user"); err != nil {
		t.Fatalf("valid scopes: %v", err)
	}
	if err := validateScopes([]string{"nope"}, "admin"); err == nil {
		t.Fatal("expected invalid scope error")
	}
	if err := validateScopes([]string{"admin"}, "user"); err == nil {
		t.Fatal("non-admin must not request admin scope")
	}
	if err := validateScopes([]string{"admin"}, "admin"); err != nil {
		t.Fatalf("admin role with admin scope: %v", err)
	}
}

func TestScopesFromForm(t *testing.T) {
	got, err := scopesFromForm([]string{"write"}, "user")
	if err != nil || len(got) != 1 || got[0] != "write" {
		t.Fatalf("scopesFromForm = %v, %v", got, err)
	}
	_, err = scopesFromForm([]string{"admin"}, "user")
	if err == nil {
		t.Fatal("expected error for admin scope by non-admin")
	}
}
