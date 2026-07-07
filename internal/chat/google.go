package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultModel   = "gemini-2.5-flash"
	defaultTemp    = 0.3
	defaultMaxTok  = 4096
	requestTimeout = 120 * time.Second
	chatEndpoint   = "/chat/completions"
)

// GoogleClient implements Client for Google AI Studio's OpenAI-compatible
// endpoint.
type GoogleClient struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewGoogleClient creates a client for the Google AI Studio OpenAI-compatible
// endpoint. baseURL should be https://generativelanguage.googleapis.com/v1beta/openai.
func NewGoogleClient(baseURL, apiKey string) *GoogleClient {
	return &GoogleClient{
		baseURL: strings.TrimRight(baseURL, "/"),
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

// HTTPError represents a non-200 HTTP response from the API.
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	switch e.StatusCode {
	case 401:
		return fmt.Sprintf("authentication failed (HTTP 401) — check your API key. Details: %s", e.Body)
	case 429:
		return "rate limited (HTTP 429) — wait and retry, or use a different provider. Free tier: ~7 questions/minute"
	case 500, 502, 503, 504:
		return fmt.Sprintf("provider server error (HTTP %d) — the service may be temporarily unavailable", e.StatusCode)
	default:
		return fmt.Sprintf("chat: HTTP %d: %s", e.StatusCode, e.Body)
	}
}

// googleChatRequest is the wire format for the OpenAI-compatible chat endpoint.
type googleChatRequest struct {
	Model       string          `json:"model"`
	Messages    []googleMessage `json:"messages"`
	Temperature float64         `json:"temperature"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

type googleMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// googleChatResponse is the wire format for the chat completion response.
type googleChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Model string `json:"model,omitempty"`
}

// googleStreamChunk is a single streaming chunk from the OpenAI-compatible SSE.
type googleStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Model string `json:"model,omitempty"`
}

// SendMessage sends a chat completion request to Google AI Studio.
func (c *GoogleClient) SendMessage(ctx context.Context, req Request) (*Response, error) {
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

	messages := make([]googleMessage, len(req.Messages))
	for i, m := range req.Messages {
		//nolint:staticcheck // struct literal intentional (different json tags)
		messages[i] = googleMessage{Role: m.Role, Content: m.Content}
	}

	payload := googleChatRequest{
		Model:       model,
		Messages:    messages,
		Temperature: temp,
		MaxTokens:   maxTok,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("chat marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+chatEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("chat request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

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

	var result googleChatResponse
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

// StreamMessage sends a streaming chat completion request.
// It returns a channel of StreamChunk that is closed when streaming completes.
func (c *GoogleClient) StreamMessage(ctx context.Context, req Request) (<-chan StreamChunk, error) {
	model := req.Model
	if model == "" {
		model = defaultModel
	}

	temp := req.Temperature
	if temp == -1 {
		temp = defaultTemp
	}

	maxTok := req.MaxTokens
	if maxTok == -1 {
		maxTok = defaultMaxTok
	}

	messages := make([]googleMessage, len(req.Messages))
	for i, m := range req.Messages {
		//nolint:staticcheck // struct literal intentional (different json tags)
		messages[i] = googleMessage{Role: m.Role, Content: m.Content}
	}

	payload := googleChatRequest{
		Model:       model,
		Messages:    messages,
		Temperature: temp,
		MaxTokens:   maxTok,
		Stream:      true,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("stream marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+chatEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("stream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("stream do: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		bodyStr := ""
		if readErr == nil {
			bodyStr = string(bodyBytes)
		}
		return nil, &HTTPError{StatusCode: resp.StatusCode, Body: bodyStr}
	}

	ch := make(chan StreamChunk)
	go func() {
		defer func() { _ = resp.Body.Close() }()
		defer close(ch)

		scanner := bufio.NewScanner(resp.Body)
		// Increase scanner buffer for safety with long data lines.
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				ch <- StreamChunk{Done: true}
				return
			}

			var streamResp googleStreamChunk
			if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
				continue // skip malformed lines
			}

			if len(streamResp.Choices) == 0 {
				continue
			}

			chunk := streamResp.Choices[0]
			content := chunk.Delta.Content
			modelName := streamResp.Model

			if content != "" {
				ch <- StreamChunk{Content: content, Model: modelName}
			}
			if chunk.FinishReason != "" {
				ch <- StreamChunk{Done: true, Model: modelName}
				return
			}
		}

		// If the stream ended without a Done signal (e.g., unexpected EOF), send Done.
		if err := scanner.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "chat stream scan error: %v\n", err)
		}
		ch <- StreamChunk{Done: true}
	}()

	return ch, nil
}
