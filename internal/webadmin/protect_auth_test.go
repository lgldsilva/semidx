package webadmin

import (
	"errors"
	"io"
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

// coverage-patch: 2026-07-17
func TestWriteUnauthorized(t *testing.T) {
	t.Run("json API", func(t *testing.T) {
		w := httptest.NewRecorder()
		a := &Admin{}
		a.writeUnauthorized(w, nil, true)
		resp := w.Result()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status = %d; want 401", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if !strings.Contains(string(body), "unauthorized") {
			t.Errorf("body = %s; want 'unauthorized'", string(body))
		}
	})
	t.Run("non-JSON (redirect)", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/admin", nil)
		a := &Admin{}
		a.writeUnauthorized(w, r, false)
		resp := w.Result()
		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("status = %d; want 303", resp.StatusCode)
		}
		loc := resp.Header.Get("Location")
		if loc != "/admin/login" {
			t.Errorf("Location = %q; want /admin/login", loc)
		}
	})
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
