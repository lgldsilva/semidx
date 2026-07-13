package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// copilotFakeServer serves the token-exchange endpoint at /token and echoes the
// inbound Authorization + integration headers on every other path. exp controls
// the token's expires_at (zero => omitted); tokenHits counts exchanges.
type copilotFakeServer struct {
	*httptest.Server
	tokenHits *atomic.Int64
}

func newCopilotFakeServer(t *testing.T, token string, exp int64, tokenStatus int) *copilotFakeServer {
	t.Helper()
	hits := &atomic.Int64{}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if got := r.Header.Get("Authorization"); got != "token gh-pat" {
			t.Errorf("exchange Authorization = %q, want %q", got, "token gh-pat")
		}
		if tokenStatus != 0 && tokenStatus != http.StatusOK {
			w.WriteHeader(tokenStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if exp > 0 {
			_, _ = fmt.Fprintf(w, `{"token":%q,"expires_at":%d}`, token, exp)
		} else {
			_, _ = fmt.Fprintf(w, `{"token":%q}`, token)
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "auth=%s intid=%s", r.Header.Get("Authorization"), r.Header.Get("Copilot-Integration-Id"))
	})
	return &copilotFakeServer{Server: httptest.NewServer(mux), tokenHits: hits}
}

func doGet(t *testing.T, d *copilotDoer, url string) (string, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := d.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return string(b), nil
}

func TestCopilotDoerInjectsBearerAndHeadersAndCaches(t *testing.T) {
	srv := newCopilotFakeServer(t, "CTOK", time.Now().Add(time.Hour).Unix(), http.StatusOK)
	defer srv.Close()
	d := newCopilotDoer("gh-pat", srv.URL+"/token", srv.Client())

	body, err := doGet(t, d, srv.URL+"/chat/completions")
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !strings.Contains(body, "auth=Bearer CTOK") {
		t.Errorf("bearer not injected: %q", body)
	}
	if !strings.Contains(body, "intid=vscode-chat") {
		t.Errorf("integration header not injected: %q", body)
	}
	// Second call reuses the cached token (no new exchange).
	if _, err := doGet(t, d, srv.URL+"/chat/completions"); err != nil {
		t.Fatalf("Do #2: %v", err)
	}
	if got := srv.tokenHits.Load(); got != 1 {
		t.Errorf("token exchanges = %d, want 1 (cached)", got)
	}
}

func TestCopilotDoerRefreshesExpiredToken(t *testing.T) {
	// expires_at already in the past → each request must re-exchange.
	srv := newCopilotFakeServer(t, "CTOK", time.Now().Add(-time.Hour).Unix(), http.StatusOK)
	defer srv.Close()
	d := newCopilotDoer("gh-pat", srv.URL+"/token", srv.Client())

	if _, err := doGet(t, d, srv.URL+"/x"); err != nil {
		t.Fatalf("Do #1: %v", err)
	}
	if _, err := doGet(t, d, srv.URL+"/x"); err != nil {
		t.Fatalf("Do #2: %v", err)
	}
	if got := srv.tokenHits.Load(); got != 2 {
		t.Errorf("token exchanges = %d, want 2 (expired each time)", got)
	}
}

func TestCopilotDoerExchangeErrorStatus(t *testing.T) {
	srv := newCopilotFakeServer(t, "", 0, http.StatusUnauthorized)
	defer srv.Close()
	d := newCopilotDoer("gh-pat", srv.URL+"/token", srv.Client())

	if _, err := doGet(t, d, srv.URL+"/x"); err == nil {
		t.Fatal("expected error on 401 token exchange")
	}
}

func TestCopilotDoerFallbackTTL(t *testing.T) {
	// No expires_at → the doer applies copilotTokenFallbackTTL and caches.
	srv := newCopilotFakeServer(t, "CTOK", 0, http.StatusOK)
	defer srv.Close()
	d := newCopilotDoer("gh-pat", srv.URL+"/token", srv.Client())

	if _, err := doGet(t, d, srv.URL+"/x"); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if _, err := doGet(t, d, srv.URL+"/x"); err != nil {
		t.Fatalf("Do #2: %v", err)
	}
	if got := srv.tokenHits.Load(); got != 1 {
		t.Errorf("token exchanges = %d, want 1 (fallback TTL cached)", got)
	}
	if time.Until(d.exp) <= copilotRefreshSkew {
		t.Errorf("fallback expiry too short: %v", time.Until(d.exp))
	}
}

func TestCopilotDoerEmptyGitHubToken(t *testing.T) {
	d := newCopilotDoer("", "http://127.0.0.1:0/token", http.DefaultClient)
	if _, _, err := d.exchange(context.Background()); err == nil {
		t.Fatal("expected error for empty GitHub token")
	}
}

// errDoer always fails, exercising the transport-error branch of exchange.
type errDoer struct{}

func (errDoer) Do(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("boom")
}

func TestCopilotDoerExchangeTransportError(t *testing.T) {
	d := newCopilotDoer("gh", "http://example.test/token", errDoer{})
	if _, _, err := d.exchange(context.Background()); err == nil {
		t.Fatal("expected transport error")
	}
}

func TestCopilotDoerExchangeBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "not-json")
	}))
	defer srv.Close()
	d := newCopilotDoer("gh", srv.URL, srv.Client())
	if _, _, err := d.exchange(context.Background()); err == nil {
		t.Fatal("expected JSON decode error")
	}
}

func TestCopilotDoerDefaults(t *testing.T) {
	d := newCopilotDoer("gh", "", nil)
	if d.tokenURL != copilotTokenURL {
		t.Errorf("default tokenURL = %q, want %q", d.tokenURL, copilotTokenURL)
	}
	if d.inner == nil {
		t.Error("default inner client must not be nil")
	}
}

func TestCopilotStaticHeaders(t *testing.T) {
	h := copilotStaticHeaders()
	for _, k := range []string{"Copilot-Integration-Id", "Editor-Version", "Editor-Plugin-Version", "Openai-Intent"} {
		if h[k] == "" {
			t.Errorf("missing static header %q", k)
		}
	}
}

func TestBuildProviderCopilot(t *testing.T) {
	p, err := BuildProvider(ProviderConfig{Type: ProviderCopilot, APIKey: "gh-pat"})
	if err != nil {
		t.Fatalf("BuildProvider(copilot): %v", err)
	}
	if p == nil {
		t.Fatal("expected a provider")
	}
	if _, err := BuildProvider(ProviderConfig{Type: ProviderCopilot}); err == nil {
		t.Fatal("expected error when Copilot has no GitHub token")
	}
}
