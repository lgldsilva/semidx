package server

import (
	"testing"
	"time"

	"github.com/lgldsilva/semidx/internal/store"
)

// TestMetricsTokenGate locks the /metrics authorization behaviour: open when no
// token is configured, Bearer-gated once SetMetricsToken is called.
func TestMetricsTokenGate(t *testing.T) {
	// No token configured → /metrics is open.
	open := New(&fakeStore{}, fakeEmbedder{}, nil)
	if rec := do(t, open, "GET", "/metrics", "", ""); rec.Code != 200 {
		t.Errorf("open metrics = %d, want 200", rec.Code)
	}

	gated := New(&fakeStore{}, fakeEmbedder{}, nil)
	gated.SetMetricsToken("s3cret")
	if rec := do(t, gated, "GET", "/metrics", "", ""); rec.Code != 401 {
		t.Errorf("metrics without token = %d, want 401", rec.Code)
	}
	if rec := do(t, gated, "GET", "/metrics", "wrong", ""); rec.Code != 401 {
		t.Errorf("metrics with wrong token = %d, want 401", rec.Code)
	}
	if rec := do(t, gated, "GET", "/metrics", "s3cret", ""); rec.Code != 200 {
		t.Errorf("metrics with right token = %d, want 200", rec.Code)
	}
}

// TestExpiredJWTRejected locks that an expired JWT control token does not
// authenticate through authed(): Verify fails, the raw string is not a stored
// opaque token, so the request is 401 — never falling through to any scope.
func TestExpiredJWTRejected(t *testing.T) {
	srv := New(&fakeStore{token: nil, project: &store.Project{Name: "p", Model: "m"}}, fakeEmbedder{}, nil)
	if err := srv.EnableJWT("test-signing-secret-0123456789"); err != nil {
		t.Fatalf("EnableJWT: %v", err)
	}
	// Minted an hour ago with a one-minute TTL → long expired.
	minted, err := srv.jwt.Mint("user", []string{"read"}, time.Minute, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if rec := do(t, srv, "POST", "/api/v1/projects/p/search", minted.Token, `{"query":"hi"}`); rec.Code != 401 {
		t.Errorf("expired JWT = %d, want 401", rec.Code)
	}
}

// TestValidJWTAuthenticates is the positive counterpart: a live JWT whose jti is
// present in the store authenticates and its scopes apply.
func TestValidJWTAuthenticates(t *testing.T) {
	srv := New(&fakeStore{
		token:   &store.Token{ID: 1, Scopes: []string{"read"}}, // jti lookup: active
		project: &store.Project{ID: 1, Name: "p", Model: "bge-m3"},
	}, fakeEmbedder{}, nil)
	if err := srv.EnableJWT("test-signing-secret-0123456789"); err != nil {
		t.Fatalf("EnableJWT: %v", err)
	}
	minted, err := srv.jwt.Mint("user", []string{"read"}, time.Hour, time.Now())
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if rec := do(t, srv, "POST", "/api/v1/projects/p/search", minted.Token, `{"query":"hi"}`); rec.Code != 200 {
		t.Errorf("valid JWT = %d, want 200", rec.Code)
	}
}

// TestReadTokenReachesAnyProject documents (and locks) the current authorization
// model: scopes are global, NOT per-project. A single read token can search any
// project it names — there is no project-ownership check. If per-project scoping
// is ever added, this test must change deliberately, flagging the behaviour shift.
func TestReadTokenReachesAnyProject(t *testing.T) {
	readToken := &store.Token{ID: 1, Scopes: []string{"read"}}
	for _, name := range []string{"alpha", "beta"} {
		srv := New(&fakeStore{
			token:   readToken,
			project: &store.Project{ID: 1, Name: name, Model: "bge-m3"},
		}, fakeEmbedder{}, nil)
		if rec := do(t, srv, "POST", "/api/v1/projects/"+name+"/search", "tok", `{"query":"x"}`); rec.Code != 200 {
			t.Errorf("read token search %q = %d, want 200 (scopes are global)", name, rec.Code)
		}
	}
}
