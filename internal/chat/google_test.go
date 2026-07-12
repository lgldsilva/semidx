package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGoogleClientSuccess(t *testing.T) {
	t.Parallel()

	var gotAuth, gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path

		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [{"message": {"content": "Hello! I can help."}}],
			"model": "gemini-2.5-flash"
		}`))
	}))
	defer srv.Close()

	c := NewGoogleClient(srv.URL, "test-key")
	resp, err := c.SendMessage(context.Background(), Request{
		Messages: []Message{
			{Role: "user", Content: "hi"},
		},
		Temperature: 0.5,
		MaxTokens:   2048,
		Model:       "gemini-2.5-flash",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	if resp.Content != "Hello! I can help." {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello! I can help.")
	}
	if resp.Model != "gemini-2.5-flash" {
		t.Errorf("Model = %q, want gemini-2.5-flash", resp.Model)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want Bearer test-key", gotAuth)
	}
	if gotPath != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions", gotPath)
	}

	// Verify the request body shape.
	if gotBody["model"] != "gemini-2.5-flash" {
		t.Errorf("model = %v, want gemini-2.5-flash", gotBody["model"])
	}
	if gotBody["temperature"] != 0.5 {
		t.Errorf("temperature = %v, want 0.5", gotBody["temperature"])
	}
	if gotBody["max_tokens"] != float64(2048) {
		t.Errorf("max_tokens = %v, want 2048", gotBody["max_tokens"])
	}
}

func TestGoogleClientDefaults(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"model":"gemini-2.5-flash"}`))
	}))
	defer srv.Close()

	c := NewGoogleClient(srv.URL, "k")
	// Empty fields should get defaults.
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

func TestGoogleClientHTTPError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := NewGoogleClient(srv.URL, "k")
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

func TestGoogleClientEmptyResponse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()

	c := NewGoogleClient(srv.URL, "k")
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

func TestGoogleClientMalformedJSON(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	c := NewGoogleClient(srv.URL, "k")
	_, err := c.SendMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestGoogleClientNoAPIKey(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("Authorization = %q, want empty for no key", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	c := NewGoogleClient(srv.URL, "")
	resp, err := c.SendMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("Content = %q, want ok", resp.Content)
	}
}

func TestGoogleClientTemperatureZeroPreserved(t *testing.T) {
	t.Parallel()

	var gotTemp float64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if t, ok := body["temperature"].(float64); ok {
			gotTemp = t
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"model":"m"}`))
	}))
	defer srv.Close()

	c := NewGoogleClient(srv.URL, "k")
	_, err := c.SendMessage(context.Background(), Request{
		Messages:    []Message{{Role: "user", Content: "hi"}},
		Temperature: 0, // explicit 0 = deterministic
		MaxTokens:   -1,
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if gotTemp != 0 {
		t.Errorf("temperature = %v, want 0 (explicit)", gotTemp)
	}
}

func TestGoogleClientTemperatureDefault(t *testing.T) {
	t.Parallel()

	var gotTemp float64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if t, ok := body["temperature"].(float64); ok {
			gotTemp = t
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"model":"m"}`))
	}))
	defer srv.Close()

	c := NewGoogleClient(srv.URL, "k")
	_, err := c.SendMessage(context.Background(), Request{
		Messages:    []Message{{Role: "user", Content: "hi"}},
		Temperature: -1, // use default
		MaxTokens:   -1,
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if gotTemp != 0.3 {
		t.Errorf("temperature = %v, want 0.3 (default)", gotTemp)
	}
}

func TestHTTPErrorUserFriendlyMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   int
		body     string
		wantPart string // part of the message we expect
	}{
		{
			name:     "401 unauthorized",
			status:   http.StatusUnauthorized,
			body:     `{"error":"invalid key"}`,
			wantPart: "authentication failed",
		},
		{
			name:     "401 also mentions check API key",
			status:   http.StatusUnauthorized,
			body:     "bad credentials",
			wantPart: "check your API key",
		},
		{
			name:     "429 rate limited",
			status:   http.StatusTooManyRequests,
			body:     "quota exceeded",
			wantPart: "rate limited",
		},
		{
			name:     "429 suggests retry",
			status:   http.StatusTooManyRequests,
			body:     "too fast",
			wantPart: "wait and retry",
		},
		{
			name:     "500 server error",
			status:   http.StatusInternalServerError,
			body:     "internal error",
			wantPart: "provider server error",
		},
		{
			name:     "502 bad gateway",
			status:   http.StatusBadGateway,
			body:     "bad gateway",
			wantPart: "provider server error",
		},
		{
			name:     "503 service unavailable",
			status:   http.StatusServiceUnavailable,
			body:     "down for maintenance",
			wantPart: "provider server error",
		},
		{
			name:     "504 gateway timeout",
			status:   http.StatusGatewayTimeout,
			body:     "upstream timeout",
			wantPart: "provider server error",
		},
		{
			name:     "other status falls back to generic",
			status:   http.StatusForbidden,
			body:     `{"error":"forbidden"}`,
			wantPart: "HTTP 403",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := &HTTPError{StatusCode: tt.status, Body: tt.body}
			msg := e.Error()
			if !strings.Contains(msg, tt.wantPart) {
				t.Errorf("Error() = %q, want to contain %q", msg, tt.wantPart)
			}
		})
	}
}

func TestGoogleClientTransportError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing listening → connection refused

	c := NewGoogleClient(url, "k")
	_, err := c.SendMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "x"}},
	})
	if err == nil {
		t.Error("transport error should propagate")
	}
}

// ---------------------------------------------------------------------------
// Streaming tests
// ---------------------------------------------------------------------------

func TestGoogleClientStreamSuccess(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify stream flag in request.
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if stream, ok := body["stream"].(bool); !ok || !stream {
			http.Error(w, "stream must be true", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		// Simulate streaming response.
		chunks := []string{"Hello", " ", "World", "!"}
		for i, c := range chunks {
			data, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{
					{
						"delta": map[string]string{"content": c},
					},
				},
				"model": "gemini-2.5-flash",
			})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			if i == 0 {
				// Send role announcement first (as real API does).
				roleData, _ := json.Marshal(map[string]any{
					"choices": []map[string]any{
						{
							"delta": map[string]string{"role": "assistant"},
						},
					},
					"model": "gemini-2.5-flash",
				})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", roleData)
				flusher.Flush()
			}
		}
		// Send termination marker.
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	c := NewGoogleClient(srv.URL, "test-key")
	ch, err := c.StreamMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
		Model:    "gemini-2.5-flash",
	})
	if err != nil {
		t.Fatalf("StreamMessage: %v", err)
	}

	var got strings.Builder
	var gotDone bool
	for chunk := range ch {
		if chunk.Done {
			gotDone = true
			if chunk.Model != "" {
				t.Logf("model in Done chunk: %s", chunk.Model)
			}
			break
		}
		got.WriteString(chunk.Content)
	}

	if !gotDone {
		t.Fatal("expected Done chunk")
	}
	if got.String() != "Hello World!" {
		t.Errorf("content = %q, want %q", got.String(), "Hello World!")
	}
}

func TestGoogleClientStreamHTTPError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := NewGoogleClient(srv.URL, "k")
	_, err := c.StreamMessage(context.Background(), Request{
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
}

func TestGoogleClientStreamContextCancellation(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		// Send chunks slowly.
		for i := 0; i < 100; i++ {
			data, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{
					{
						"delta": map[string]string{"content": "chunk "},
					},
				},
			})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			time.Sleep(10 * time.Millisecond)
		}
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := NewGoogleClient(srv.URL, "k")
	ch, err := c.StreamMessage(ctx, Request{
		Messages: []Message{{Role: "user", Content: "x"}},
	})
	if err != nil {
		t.Fatalf("StreamMessage: %v", err)
	}

	// Read a few chunks, then cancel.
	count := 0
	for chunk := range ch {
		if chunk.Done {
			break
		}
		count++
		if count >= 3 {
			cancel()
		}
	}

	if count < 3 {
		t.Errorf("expected at least 3 chunks before cancellation, got %d", count)
	}
}

func TestGoogleClientStreamTransportError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // connection refused

	c := NewGoogleClient(url, "k")
	_, err := c.StreamMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "x"}},
	})
	if err == nil {
		t.Error("transport error should propagate")
	}
}

func TestGoogleClientStreamEmptyChoices(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		// Send chunk with empty choices (should be skipped).
		data, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{},
			"model":   "gemini-2.5-flash",
		})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	c := NewGoogleClient(srv.URL, "k")
	ch, err := c.StreamMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "x"}},
	})
	if err != nil {
		t.Fatalf("StreamMessage: %v", err)
	}

	var gotContent string
	var gotDone bool
	for chunk := range ch {
		if chunk.Done {
			gotDone = true
		} else {
			gotContent += chunk.Content
		}
	}

	if !gotDone {
		t.Error("expected Done chunk")
	}
	if gotContent != "" {
		t.Errorf("expected empty content, got %q", gotContent)
	}
}

func TestGoogleClientStreamFinishReason(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		// Content chunk.
		data1, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{
				{
					"delta": map[string]string{"content": "Hello"},
				},
			},
			"model": "gemini-2.5-flash",
		})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data1)
		flusher.Flush()

		// Final chunk with finish_reason (no [DONE]).
		data2, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{
				{
					"delta":         map[string]string{},
					"finish_reason": "stop",
				},
			},
			"model": "gemini-2.5-flash",
		})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data2)
		flusher.Flush()
	}))
	defer srv.Close()

	c := NewGoogleClient(srv.URL, "k")
	ch, err := c.StreamMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "x"}},
	})
	if err != nil {
		t.Fatalf("StreamMessage: %v", err)
	}

	var gotContent string
	var gotDone bool
	for chunk := range ch {
		if chunk.Done {
			gotDone = true
		} else {
			gotContent += chunk.Content
		}
	}

	if !gotDone {
		t.Error("expected Done chunk via finish_reason")
	}
	if gotContent != "Hello" {
		t.Errorf("content = %q, want %q", gotContent, "Hello")
	}
}

// ---------------------------------------------------------------------------
// Tool calling tests
// ---------------------------------------------------------------------------

func TestGoogleClientToolCallNonStreaming(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify tools were sent in the request body.
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		tools, ok := body["tools"].([]any)
		if !ok || len(tools) == 0 {
			http.Error(w, "missing tools", http.StatusBadRequest)
			return
		}
		tool0 := tools[0].(map[string]any)
		if tool0["type"] != "function" {
			t.Errorf("tool type = %v, want 'function'", tool0["type"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {
					"content": null,
					"tool_calls": [
						{
							"id": "call_abc123",
							"type": "function",
							"function": {
								"name": "get_weather",
								"arguments": "{\"location\": \"Paris\"}"
							}
						}
					]
				}
			}],
			"model": "gemini-2.5-flash"
		}`))
	}))
	defer srv.Close()

	c := NewGoogleClient(srv.URL, "test-key")
	resp, err := c.SendMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "weather in Paris?"}},
		Tools: []ToolDef{
			{
				Name:        "get_weather",
				Description: "Get weather for a location",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	if resp.Content != "" {
		t.Errorf("Content = %q, want empty for tool call", resp.Content)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ID != "call_abc123" {
		t.Errorf("ToolCall ID = %q, want %q", resp.ToolCalls[0].ID, "call_abc123")
	}
	if resp.ToolCalls[0].Name != "get_weather" {
		t.Errorf("ToolCall Name = %q, want %q", resp.ToolCalls[0].Name, "get_weather")
	}
	if resp.ToolCalls[0].Args != `{"location": "Paris"}` {
		t.Errorf("ToolCall Args = %q, want %q", resp.ToolCalls[0].Args, `{"location": "Paris"}`)
	}
}

func TestGoogleClientToolCallStreaming(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		// Simulate SSE chunks that emit a tool call.
		// Chunk 1: tool call header (id + name).
		chunk1, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{
				{
					"delta": map[string]any{
						"tool_calls": []map[string]any{
							{
								"index": 0,
								"id":    "call_stream_1",
								"type":  "function",
								"function": map[string]string{
									"name":      "search_code",
									"arguments": "",
								},
							},
						},
					},
				},
			},
			"model": "gemini-2.5-flash",
		})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk1)
		flusher.Flush()

		// Chunk 2: argument delta.
		chunk2, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{
				{
					"delta": map[string]any{
						"tool_calls": []map[string]any{
							{
								"index": 0,
								"function": map[string]string{
									"arguments": "{\"query\": \"semantic",
								},
							},
						},
					},
				},
			},
		})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk2)
		flusher.Flush()

		// Chunk 3: final args delta.
		chunk3, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{
				{
					"delta": map[string]any{
						"tool_calls": []map[string]any{
							{
								"index": 0,
								"function": map[string]string{
									"arguments": " search\"}",
								},
							},
						},
					},
				},
			},
		})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk3)
		flusher.Flush()

		// Chunk 4: finish reason.
		chunk4, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{
				{
					"delta":         map[string]any{},
					"finish_reason": "tool_calls",
				},
			},
			"model": "gemini-2.5-flash",
		})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk4)
		flusher.Flush()
	}))
	defer srv.Close()

	c := NewGoogleClient(srv.URL, "test-key")
	ch, err := c.StreamMessage(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "search for x"}},
		Tools: []ToolDef{
			{
				Name:        "search_code",
				Description: "Search codebase",
				Parameters:  map[string]any{"type": "object"},
			},
		},
	})
	if err != nil {
		t.Fatalf("StreamMessage: %v", err)
	}

	var gotToolCalls []ToolCall
	var gotDone bool
	for chunk := range ch {
		if chunk.Done {
			gotDone = true
			if len(chunk.ToolCalls) > 0 {
				gotToolCalls = chunk.ToolCalls
			}
			break
		}
		if len(chunk.ToolCalls) > 0 {
			gotToolCalls = chunk.ToolCalls
		}
	}

	if !gotDone {
		t.Fatal("expected Done chunk")
	}
	if len(gotToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(gotToolCalls))
	}
	if gotToolCalls[0].ID != "call_stream_1" {
		t.Errorf("ToolCall ID = %q, want %q", gotToolCalls[0].ID, "call_stream_1")
	}
	if gotToolCalls[0].Name != "search_code" {
		t.Errorf("ToolCall Name = %q, want %q", gotToolCalls[0].Name, "search_code")
	}
	if gotToolCalls[0].Args != `{"query": "semantic search"}` {
		t.Errorf("ToolCall Args = %q, want %q", gotToolCalls[0].Args, `{"query": "semantic search"}`)
	}
}

func TestGoogleClientSupportsTools(t *testing.T) {
	c := NewGoogleClient("http://example.com", "k")
	if !c.SupportsTools() {
		t.Error("GoogleClient should support tools")
	}
}

// Ensure GoogleClient implements StreamClient.
var _ StreamClient = (*GoogleClient)(nil)
