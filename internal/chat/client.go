// Package chat defines the LLM chat client interface and shared types used by
// the ChatRAG pipeline.
package chat

import (
	"context"
	"encoding/json"
)

// Client is a chat LLM provider.
type Client interface {
	SendMessage(ctx context.Context, req Request) (*Response, error)
}

// StreamChunk is a single chunk from a streaming chat response.
type StreamChunk struct {
	Content   string     // delta content (empty for final/tool chunks)
	ToolCalls []ToolCall // tool calls from the model (nil for non-tool chunks)
	Done      bool       // true on the final (termination) chunk
	Model     string     // model name (only set on first or last chunk)
	// Sources/Fallback carry citations on the terminal chunk for streams that
	// only learn their sources after the fact (the agent, which runs tool calls
	// before answering). Empty for the classic RAG stream, which returns sources
	// up front instead.
	Sources  []Source
	Fallback bool
	// Tool is a mid-stream tool activity event (agent streams only); nil on
	// text/terminal chunks.
	Tool *ToolEvent
	// Err carries a sanitized, user-safe message on the terminal chunk when the
	// stream failed. It must never echo the provider's raw error (which can
	// contain URLs or keys) — that goes to the server log only.
	Err string
}

// ToolEvent kinds carried by StreamChunk.Tool.
const (
	ToolEventCall   = "call"   // the model committed to a tool call
	ToolEventResult = "result" // the tool finished
)

// ToolEvent reports one tool call or tool result inside a streamed answer.
// Args carries the sanitized JSON arguments (calls only); Preview a bounded
// excerpt of the output (results only).
type ToolEvent struct {
	Kind      string // ToolEventCall | ToolEventResult
	ID        string // provider tool-call id (correlates call ↔ result)
	Name      string
	Args      json.RawMessage
	Preview   string
	IsError   bool
	ElapsedMS int64
	Truncated bool
}

// Source is one retrieved chunk cited by an answer. It mirrors rag.Source but
// lives in this package so StreamChunk can carry it without importing rag
// (which would be a cycle).
type Source struct {
	File      string
	StartLine int
	EndLine   int
	Content   string
	Score     float64
	Keyword   bool
	Project   string // source project label; set only by global (all-projects) chat
}

// StreamClient is a chat provider that supports streaming responses.
type StreamClient interface {
	Client
	// StreamMessage sends a chat request and returns chunks as they arrive.
	// The returned channel is closed when streaming completes.
	StreamMessage(ctx context.Context, req Request) (<-chan StreamChunk, error)
}

// Message is a single chat message. For tool calling, an assistant message may
// carry ToolCalls, and a "tool" message must carry the ToolCallID it answers.
type Message struct {
	Role       string     `json:"role"` // "system", "user", "assistant", "tool"
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // set on assistant messages that call tools
	ToolCallID string     `json:"tool_call_id,omitempty"` // set on tool messages (which call they answer)
}

// ToolDef is an OpenAI/Gemini function declaration for tool calling.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON schema of arguments
}

// ToolCall is one call the model wants to make.
type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"arguments"` // raw JSON arguments string
}

// ToolResult is the result of executing a tool call, sent back to the model.
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Content    string `json:"content"` // result text
}

// Request is a chat completion request.
type Request struct {
	Messages    []Message
	Tools       []ToolDef // tool definitions (nil/empty = no tool calling)
	Temperature float64   // 0.0-1.0
	MaxTokens   int       // 0 = model default
	Model       string    // e.g. "gemini-2.5-flash"
}

// Response is a chat completion response.
type Response struct {
	Content   string     // response text (empty if only tool calls)
	ToolCalls []ToolCall // tool calls from the model
	// Model used (may differ from request if fallback happened).
	Model string
}
