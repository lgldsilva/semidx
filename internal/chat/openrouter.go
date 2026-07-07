package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// openRouterChatRequest is the wire format for the OpenAI-compatible chat endpoint.
type openRouterChatRequest struct {
	Model       string          `json:"model"`
	Messages    []openRouterMsg `json:"messages"`
	Temperature float64         `json:"temperature"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
}

type openRouterMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// openRouterChatResponse is the wire format for the chat completion response.
type openRouterChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Model string `json:"model,omitempty"`
}

// SendMessage sends a chat completion request to OpenRouter.
func (c *OpenRouterClient) SendMessage(ctx context.Context, req Request) (*Response, error) {
	model := req.Model
	if model == "" {
		model = defaultModel
	}

	// Temperature: -1 means "use default", 0 is a valid explicit value.
	temp := req.Temperature
	if temp == -1 {
		temp = defaultTemp
	}

	// MaxTokens: -1 means "use default", 0 means "model default" (omit from wire).
	maxTok := req.MaxTokens
	if maxTok == -1 {
		maxTok = defaultMaxTok
	}

	messages := make([]openRouterMsg, len(req.Messages))
	for i, m := range req.Messages {
		//nolint:staticcheck // struct literal intentional (different json tags)
		messages[i] = openRouterMsg{Role: m.Role, Content: m.Content}
	}

	payload := openRouterChatRequest{
		Model:       model,
		Messages:    messages,
		Temperature: temp,
		MaxTokens:   maxTok,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("chat marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+openRouterEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("chat request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	// OpenRouter requires HTTP-Referer header.
	httpReq.Header.Set("HTTP-Referer", openRouterReferer)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("chat do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// Bounded read to prevent OOM from malicious/large error bodies.
		bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		bodyStr := ""
		if readErr == nil {
			bodyStr = string(bodyBytes)
		}
		return nil, &HTTPError{StatusCode: resp.StatusCode, Body: bodyStr}
	}

	var result openRouterChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("chat decode: %w", err)
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("chat: no choices in response")
	}

	return &Response{
		Content: result.Choices[0].Message.Content,
		Model:   result.Model,
	}, nil
}
