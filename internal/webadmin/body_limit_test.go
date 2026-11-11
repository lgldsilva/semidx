package webadmin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Regression: /admin JSON handlers decoded unbounded bodies.
func TestAdminLoginBodyIsCapped(t *testing.T) {
	a, err := New(newFakeStore(), fakeEmbedder{}, nil, true, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	big := `{"username":"x","password":"` + strings.Repeat("a", 64<<10) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/login", strings.NewReader(big))
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge && rec.Code != http.StatusBadRequest {
		t.Fatalf("64 KiB login body = %d, want 413/400", rec.Code)
	}
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("oversized body was fully decoded")
	}
}

func TestAdminBodyLimitByRoute(t *testing.T) {
	cases := map[string]int64{
		"/admin/api/login":                    adminLoginMaxBody,
		"/admin/api/projects/p/files/batch":   adminBatchMaxBody,
		"/admin/api/projects/p/files/archive": adminArchiveMaxBody,
		"/admin/api/projects":                 adminDefaultMaxBody,
		"/admin/api/projects/p/chat/stream":   adminDefaultMaxBody,
	}
	for p, want := range cases {
		if got := adminBodyLimit(p); got != want {
			t.Errorf("adminBodyLimit(%q) = %d, want %d", p, got, want)
		}
	}
}
