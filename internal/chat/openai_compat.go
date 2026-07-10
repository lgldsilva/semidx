package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Shared wire types and helpers for OpenAI-compatible chat completion providers
// (Google AI Studio, OpenRouter, …). Providers differ only in base URL and a
// couple of headers, so the request/response shapes and the send flow live here
// instead of being copy-pasted per provider.

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature float64         `json:"temperature"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Model string `json:"model,omitempty"`
}

// resolveChatDefaults applies the shared "-1 means use default" sentinel
// handling. Temperature 0 and MaxTokens 0 remain valid explicit values.
func resolveChatDefaults(req Request) (model string, temp float64, maxTok int) {
	model = req.Model
	if model == "" {
		model = defaultModel
	}
	temp = req.Temperature
	if temp == -1 {
		temp = defaultTemp
	}
	maxTok = req.MaxTokens
	if maxTok == -1 {
		maxTok = defaultMaxTok
	}
	return model, temp, maxTok
}

// buildOpenAIChatRequest assembles the shared payload for req.
func buildOpenAIChatRequest(req Request, stream bool) openAIChatRequest {
	model, temp, maxTok := resolveChatDefaults(req)
	messages := make([]openAIMessage, len(req.Messages))
	for i, m := range req.Messages {
		messages[i] = openAIMessage(m)
	}
	return openAIChatRequest{
		Model:       model,
		Messages:    messages,
		Temperature: temp,
		MaxTokens:   maxTok,
		Stream:      stream,
	}
}

// sendOpenAIChat performs a non-streaming OpenAI-compatible chat completion.
// extraHeaders is applied after the standard Content-Type/Authorization headers.
func sendOpenAIChat(ctx context.Context, hc *http.Client, url, apiKey string, extraHeaders map[string]string, req Request) (*Response, error) {
	body, err := json.Marshal(buildOpenAIChatRequest(req, false))
	if err != nil {
		return nil, fmt.Errorf("chat marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("chat request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	for k, v := range extraHeaders {
		httpReq.Header.Set(k, v)
	}

	resp, err := hc.Do(httpReq)
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

	var result openAIChatResponse
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
