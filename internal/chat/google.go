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

// googleStreamChunk is a single streaming chunk from the OpenAI-compatible SSE.
type googleStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string                `json:"content"`
			ToolCalls []openAIDeltaToolCall `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Model string `json:"model,omitempty"`
}

// SupportsTools reports whether Google AI Studio supports tool calling.
func (c *GoogleClient) SupportsTools() bool { return true }

// SendMessage sends a chat completion request to Google AI Studio.
func (c *GoogleClient) SendMessage(ctx context.Context, req Request) (*Response, error) {
	return sendOpenAIChat(ctx, c.client, c.baseURL+chatEndpoint, c.apiKey, nil, req)
}

// StreamMessage sends a streaming chat completion request.
// It returns a channel of StreamChunk that is closed when streaming completes.
func (c *GoogleClient) StreamMessage(ctx context.Context, req Request) (<-chan StreamChunk, error) {
	payload, err := c.buildStreamPayload(req)
	if err != nil {
		return nil, fmt.Errorf("stream marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+chatEndpoint, bytes.NewReader(payload))
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
	go streamSSEResponse(ctx, resp, ch)

	return ch, nil
}

func (c *GoogleClient) buildStreamPayload(req Request) ([]byte, error) {
	return json.Marshal(buildOpenAIChatRequest(req, true))
}

func streamSSEResponse(ctx context.Context, resp *http.Response, ch chan<- StreamChunk) {
	defer close(ch)
	defer func() { _ = resp.Body.Close() }()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// Accumulator for streaming tool calls, keyed by index.
	toolAcc := make(map[int]*ToolCall)

	for scanner.Scan() {
		if processStreamLine(ctx, scanner.Text(), toolAcc, ch) {
			return
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "chat stream scan error: %v\n", err)
	}
	if len(toolAcc) > 0 {
		select {
		case ch <- StreamChunk{ToolCalls: finalizeToolCalls(toolAcc), Done: true}:
		case <-ctx.Done():
		}
		return
	}
	select {
	case ch <- StreamChunk{Done: true}:
	case <-ctx.Done():
	}
}

// processStreamLine handles one SSE data line. Returns true when streaming is
// complete (either [DONE] or a finish_reason was received).
func processStreamLine(ctx context.Context, line string, toolAcc map[int]*ToolCall, ch chan<- StreamChunk) bool {
	if !strings.HasPrefix(line, "data: ") {
		return false
	}
	data := strings.TrimPrefix(line, "data: ")
	if data == "[DONE]" {
		if len(toolAcc) > 0 {
			select {
			case ch <- StreamChunk{ToolCalls: finalizeToolCalls(toolAcc), Done: true}:
			case <-ctx.Done():
			}
		} else {
			select {
			case ch <- StreamChunk{Done: true}:
			case <-ctx.Done():
			}
		}
		return true
	}

	var streamResp googleStreamChunk
	if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
		return false
	}

	if len(streamResp.Choices) == 0 {
		return false
	}

	chunk := streamResp.Choices[0]
	content := chunk.Delta.Content
	modelName := streamResp.Model

	handleToolCallDelta(chunk.Delta.ToolCalls, toolAcc)

	if content != "" {
		select {
		case ch <- StreamChunk{Content: content, Model: modelName}:
		case <-ctx.Done():
		}
	}

	if chunk.FinishReason != "" {
		if len(toolAcc) > 0 {
			select {
			case ch <- StreamChunk{ToolCalls: finalizeToolCalls(toolAcc), Done: true, Model: modelName}:
			case <-ctx.Done():
			}
		} else {
			select {
			case ch <- StreamChunk{Done: true, Model: modelName}:
			case <-ctx.Done():
			}
		}
		return true
	}

	return false
}

// handleToolCallDelta accumulates streaming tool call deltas into the
// accumulator map, keyed by the delta's index.
func handleToolCallDelta(deltas []openAIDeltaToolCall, toolAcc map[int]*ToolCall) {
	for _, dtc := range deltas {
		tc, ok := toolAcc[dtc.Index]
		if !ok {
			tc = &ToolCall{ID: dtc.ID, Name: ""}
			toolAcc[dtc.Index] = tc
		}
		if dtc.ID != "" {
			tc.ID = dtc.ID
		}
		if dtc.Function != nil {
			if dtc.Function.Name != "" {
				tc.Name = dtc.Function.Name
			}
			tc.Args += dtc.Function.Arguments
		}
	}
}

// finalizeToolCalls converts the accumulated tool call map into a sorted slice.
func finalizeToolCalls(acc map[int]*ToolCall) []ToolCall {
	if len(acc) == 0 {
		return nil
	}
	maxIdx := 0
	for idx := range acc {
		if idx > maxIdx {
			maxIdx = idx
		}
	}
	out := make([]ToolCall, 0, len(acc))
	for i := 0; i <= maxIdx; i++ {
		if tc, ok := acc[i]; ok {
			out = append(out, *tc)
		}
	}
	return out
}
