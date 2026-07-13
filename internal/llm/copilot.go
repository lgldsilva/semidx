package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// GitHub Copilot exposes an OpenAI-compatible chat API, but authentication is
// two-legged: a long-lived GitHub token is exchanged for a short-lived Copilot
// token (~30 min) that authorizes the chat endpoint, plus a set of editor
// integration headers. Because the chat token expires mid-session, auth is
// injected per-request by copilotDoer rather than via a static WithAPIKey.
const (
	copilotDefaultAPIBase = "https://api.githubcopilot.com"
	// #nosec G101 -- this is the public Copilot token-exchange endpoint URL, not a credential.
	copilotTokenURL = "https://api.github.com/copilot_internal/v2/token"
	// copilotIntegrationID identifies the caller to the Copilot chat backend.
	copilotIntegrationID = "vscode-chat"
	// copilotEditorVersion is sent as the Editor-Version/Editor-Plugin-Version
	// the Copilot API requires; the value is informational.
	copilotEditorVersion = "semidx/1.0"
	// copilotRefreshSkew renews the token slightly before it actually expires.
	copilotRefreshSkew = 60 * time.Second
	// copilotTokenFallbackTTL is used when the exchange omits expires_at.
	copilotTokenFallbackTTL = 25 * time.Minute
)

// copilotStaticHeaders are sent on every Copilot API request alongside the
// short-lived bearer token.
func copilotStaticHeaders() map[string]string {
	return map[string]string{
		"Copilot-Integration-Id": copilotIntegrationID,
		"Editor-Version":         copilotEditorVersion,
		"Editor-Plugin-Version":  copilotEditorVersion,
		"Openai-Intent":          "conversation-panel",
	}
}

// httpDoer is the minimal HTTP surface satisfied by *http.Client and by
// openaicompat's option.HTTPClient — Do(*http.Request) (*http.Response, error).
type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// copilotDoer authenticates each Copilot API request with a token exchanged
// from a GitHub token, refreshing it transparently on expiry. It implements the
// HTTP client interface openaicompat.WithHTTPClient accepts, so the whole
// two-legged auth is invisible to the fantasy provider layer.
type copilotDoer struct {
	githubToken string
	tokenURL    string
	inner       httpDoer

	mu    sync.Mutex
	token string
	exp   time.Time
}

// newCopilotDoer builds the doer. An empty tokenURL uses the public GitHub
// endpoint; a nil inner uses a default *http.Client.
func newCopilotDoer(githubToken, tokenURL string, inner httpDoer) *copilotDoer {
	if tokenURL == "" {
		tokenURL = copilotTokenURL
	}
	if inner == nil {
		inner = &http.Client{Timeout: 120 * time.Second}
	}
	return &copilotDoer{githubToken: githubToken, tokenURL: tokenURL, inner: inner}
}

// Do injects the short-lived Copilot bearer token and integration headers, then
// delegates to the inner client. A failed token exchange fails the request.
func (d *copilotDoer) Do(req *http.Request) (*http.Response, error) {
	tok, err := d.ensureToken(req.Context())
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	for k, v := range copilotStaticHeaders() {
		req.Header.Set(k, v)
	}
	return d.inner.Do(req)
}

// ensureToken returns a valid Copilot token, exchanging or refreshing when the
// cached one is missing or within copilotRefreshSkew of expiry.
func (d *copilotDoer) ensureToken(ctx context.Context) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.token != "" && time.Until(d.exp) > copilotRefreshSkew {
		return d.token, nil
	}
	tok, exp, err := d.exchange(ctx)
	if err != nil {
		return "", err
	}
	d.token, d.exp = tok, exp
	return tok, nil
}

// copilotTokenResponse is the subset of the exchange payload semidx consumes.
type copilotTokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
}

// exchange trades the GitHub token for a short-lived Copilot token.
func (d *copilotDoer) exchange(ctx context.Context) (string, time.Time, error) {
	if d.githubToken == "" {
		return "", time.Time{}, fmt.Errorf("copilot: missing GitHub token")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.tokenURL, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Authorization", "token "+d.githubToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Editor-Version", copilotEditorVersion)
	req.Header.Set("Editor-Plugin-Version", copilotEditorVersion)

	resp, err := d.inner.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("copilot token exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", time.Time{}, fmt.Errorf("copilot token exchange: unexpected status %d", resp.StatusCode)
	}
	var tr copilotTokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", time.Time{}, fmt.Errorf("copilot token exchange: decode: %w", err)
	}
	if tr.Token == "" {
		return "", time.Time{}, fmt.Errorf("copilot token exchange: empty token in response")
	}
	exp := time.Now().Add(copilotTokenFallbackTTL)
	if tr.ExpiresAt > 0 {
		exp = time.Unix(tr.ExpiresAt, 0)
	}
	return tr.Token, exp, nil
}
