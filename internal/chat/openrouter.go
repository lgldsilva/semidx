package chat

import (
	"context"
	"net/http"
	"strings"
	"time"
)

const (
	defaultOpenRouterURL = "https://openrouter.ai/api/v1"
	openRouterEndpoint   = "/chat/completions"
	openRouterReferer    = "https://github.com/lgldsilva/semidx"
)

// OpenRouterClient implements Client for the OpenRouter API (OpenAI-compatible).
type OpenRouterClient struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewOpenRouterClient creates a client for the OpenRouter API.
// baseURL defaults to https://openrouter.ai/api/v1 when empty.
func NewOpenRouterClient(baseURL, apiKey string) *OpenRouterClient {
	u := strings.TrimRight(baseURL, "/")
	if u == "" {
		u = defaultOpenRouterURL
	}
	return &OpenRouterClient{
		baseURL: u,
		apiKey:  apiKey,
		client: &http.Client{
			Timeout: requestTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// SendMessage sends a chat completion request to OpenRouter. OpenRouter
// requires an HTTP-Referer header in addition to the standard ones.
func (c *OpenRouterClient) SendMessage(ctx context.Context, req Request) (*Response, error) {
	return sendOpenAIChat(ctx, c.client, c.baseURL+openRouterEndpoint, c.apiKey,
		map[string]string{"HTTP-Referer": openRouterReferer}, req)
}
