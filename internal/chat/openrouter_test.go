package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenRouterClientSuccess(t *testing.T) {
	t.Parallel()

	var gotAuth, gotPath, gotReferer string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotReferer = r.Header.Get("HTTP-Referer")

		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [{"message": {"content": "Hello via OpenRouter!"}}],
			"model": "openrouter-model"
		}`))
	}))
	defer srv.Close()

	c := NewOpenRouterClient(srv.URL, "or-key")
	resp, err := c.SendMessage(context.Background(), Request{
		Messages: []Message{
			{Role: "user", Content: "hi"},
		},
		Temperature: 0.5,
		MaxTokens:   2048,
		Model:       "anthropic/claude-sonnet",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	if resp.Content != "Hello via OpenRouter!" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello via OpenRouter!")
	}
	if resp.Model != "openrouter-model" {
		t.Errorf("Model = %q, want openrouter-model", resp.Model)
	}
	if gotAuth != "Bearer or-key" {
		t.Errorf("Authorization = %q, want Bearer or-key", gotAuth)
	}
	if gotPath != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions", gotPath)
	}
	if gotReferer != "https://github.com/lgldsilva/semidx" {
		t.Errorf("HTTP-Referer = %q, want https://github.com/lgldsilva/semidx", gotReferer)
	}

	if gotBody["model"] != "anthropic/claude-sonnet" {
		t.Errorf("model = %v, want anthropic/claude-sonnet", gotBody["model"])
	}
	if gotBody["temperature"] != 0.5 {
		t.Errorf("temperature = %v, want 0.5", gotBody["temperature"])
	}
	if gotBody["max_tokens"] != float64(2048) {
		t.Errorf("max_tokens = %v, want 2048", gotBody["max_tokens"])
	}
}

func TestOpenRouterClientDefaultBaseURL(t *testing.T) {
	t.Parallel()

	c := NewOpenRouterClient("", "key")
	if c.baseURL != defaultOpenRouterURL {
		t.Errorf("baseURL = %q, want %q", c.baseURL, defaultOpenRouterURL)
	}
}

func TestOpenRouterClientHTTPError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := NewOpenRouterClient(srv.URL, "k")
	_, err := c.SendMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected error for non-200")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err type = %T, want *HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, want %d", httpErr.StatusCode, http.StatusTooManyRequests)
	}
	if !strings.Contains(httpErr.Body, "rate limited") {
		t.Errorf("Body = %q, want to contain 'rate limited'", httpErr.Body)
	}
}

func TestOpenRouterClientMalformedJSON(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	c := NewOpenRouterClient(srv.URL, "k")
	_, err := c.SendMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestOpenRouterClientEmptyResponse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()

	c := NewOpenRouterClient(srv.URL, "k")
	_, err := c.SendMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
	if !strings.Contains(err.Error(), "no choices") {
		t.Errorf("err = %v, want 'no choices'", err)
	}
}

func TestOpenRouterClientDefaults(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"model":"m"}`))
	}))
	defer srv.Close()

	c := NewOpenRouterClient(srv.URL, "k")
	resp, err := c.SendMessage(context.Background(), Request{
		Messages:    []Message{{Role: "user", Content: "hello"}},
		Temperature: -1,
		MaxTokens:   -1,
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("Content = %q", resp.Content)
	}
}

func TestOpenRouterClientTransportError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing listening → connection refused

	c := NewOpenRouterClient(url, "k")
	_, err := c.SendMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "x"}},
	})
	if err == nil {
		t.Error("transport error should propagate")
	}
}
