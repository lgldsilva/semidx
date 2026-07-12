package webadmin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProtectAuthWriters(t *testing.T) {
	rec := httptest.NewRecorder()
	a := &Admin{}
	a.writeInternalError(rec, true)
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), "internal error") {
		t.Fatalf("internal json = %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	a.writeInternalError(rec, false)
	if rec.Code != 500 {
		t.Fatalf("internal html = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	a.writeCSRFError(rec, true)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "CSRF") {
		t.Fatalf("csrf json = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	a.writeCSRFError(rec, false)
	if rec.Code != 403 {
		t.Fatalf("csrf html = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	a.writeRoleError(rec, true)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "admin only") {
		t.Fatalf("role json = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	a.writeRoleError(rec, false)
	if rec.Code != 403 {
		t.Fatalf("role html = %d", rec.Code)
	}
}

func TestResolveAuthCtxSessionError(t *testing.T) {
	fs := newFakeStore()
	fs.sessionErr = errors.New("db down")
	a, _ := New(fs, fakeEmbedder{}, nil, true, nil, "")
	fs.addUser("admin", "supersecret", "admin")
	fs.sessions[hashToken("tok")] = 1

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/api/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "tok"})
	if _, ok := a.resolveAuthCtx(rec, req, true); ok {
		t.Fatal("expected auth failure")
	}
	if rec.Code != 500 {
		t.Fatalf("session err = %d body=%s", rec.Code, rec.Body.String())
	}
}
