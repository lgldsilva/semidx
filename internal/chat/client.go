// Package chat defines the LLM chat client interface and shared types used by
// the ChatRAG pipeline.
package chat

import "context"

// Client is a chat LLM provider.
type Client interface {
	SendMessage(ctx context.Context, req Request) (*Response, error)
}

// StreamChunk is a single chunk from a streaming chat response.
type StreamChunk struct {
	Content string // delta content (empty for final chunk)
	Done    bool   // true on the final (termination) chunk
	Model   string // model name (only set on first or last chunk)
}

// StreamClient is a chat provider that supports streaming responses.
type StreamClient interface {
	Client
	// StreamMessage sends a chat request and returns chunks as they arrive.
	// The returned channel is closed when streaming completes.
	StreamMessage(ctx context.Context, req Request) (<-chan StreamChunk, error)
}

// Message is a single chat message.
type Message struct {
	Role    string `json:"role"` // "system", "user", "assistant"
	Content string `json:"content"`
}

// Request is a chat completion request.
type Request struct {
	Messages    []Message
	Temperature float64 // 0.0-1.0
	MaxTokens   int     // 0 = model default
	Model       string  // e.g. "gemini-2.5-flash"
}

// Response is a chat completion response.
type Response struct {
	Content string
	// Model used (may differ from request if fallback happened).
	Model string
}
