package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// NamedClient pairs a name with a chat client for use in the chain.
type NamedClient struct {
	Name   string
	Client Client
}

// ProviderDiagnostic holds structured info about what each provider returned.
type ProviderDiagnostic struct {
	Name  string
	Error string
}

// ChainClient tries chat providers in order, falling through on failure.
type ChainClient struct {
	providers []NamedClient

	// OnFallback is called when a provider fails and the chain moves to the
	// next one. The caller (CLI, web, MCP) sets this to log or display the
	// fallback notice. Must be safe to call concurrently.
	OnFallback func(name string, err error)
}

// NewChain creates a chain with the given ordered providers.
func NewChain(providers ...NamedClient) *ChainClient {
	return &ChainClient{providers: providers}
}

// SendMessage tries each provider in order. On non-200 HTTP status (especially
// 429/5xx) it tries the next provider. Returns the first successful response.
// Returns the last error if all providers fail. Safe for concurrent use.
func (cc *ChainClient) SendMessage(ctx context.Context, req Request) (*Response, error) {
	var providerErrors []ProviderDiagnostic // local — no shared mutable state
	var lastErr error
	for _, p := range cc.providers {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		result, err := p.Client.SendMessage(ctx, req)
		if err == nil {
			// Preserve the model from the actual responding provider.
			if result.Model == "" {
				result.Model = req.Model
			}
			return result, nil
		}

		lastErr = err
		providerErrors = append(providerErrors, ProviderDiagnostic{
			Name:  p.Name,
			Error: err.Error(),
		})

		// Report fallback to the caller if a callback is configured.
		cc.reportFallback(p.Name, err)

		// Context cancellation short-circuits.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Continue to next provider regardless of error type.
		// 429/5xx HTTP errors, transport errors, decode errors — all cause
		// fallthrough.
	}

	if lastErr == nil {
		return nil, fmt.Errorf("chain: no chat providers configured — set GEMINI_API_KEY or OPENROUTER_API_KEY")
	}

	return nil, buildChainError(providerErrors, lastErr)
}

// reportFallback notifies the caller of a provider failure if a callback is
// configured. Safe to call when OnFallback is nil.
func (cc *ChainClient) reportFallback(name string, err error) {
	if cc.OnFallback != nil {
		cc.OnFallback(name, err)
	}
}

// buildChainError assembles an actionable summary of all provider errors,
// keeping %w on the last error so callers can use errors.As to inspect
// HTTPError etc.
func buildChainError(providerErrors []ProviderDiagnostic, lastErr error) error {
	var b strings.Builder
	b.WriteString("chain: all chat providers failed.\n")
	for _, d := range providerErrors {
		fmt.Fprintf(&b, "- %s: %s\n", d.Name, d.Error)
	}
	b.WriteString("Check your API keys: GEMINI_API_KEY and OPENROUTER_API_KEY.")
	return fmt.Errorf("%s (%w)", strings.TrimSpace(b.String()), lastErr)
}

// StreamMessage tries providers in order, falling through on errors.
// If a provider does not implement StreamClient, it falls back to non-streaming
// SendMessage and wraps the result as a single chunk + Done.
func (cc *ChainClient) StreamMessage(ctx context.Context, req Request) (<-chan StreamChunk, error) {
	var lastErr error

	for _, p := range cc.providers {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		chunks, err := cc.tryStreamProvider(ctx, p, req)
		if err == nil {
			return chunks, nil
		}
		lastErr = err
		cc.reportFallback(p.Name, err)
	}

	return nil, fmt.Errorf("chain: all streaming providers failed (%w)", lastErr)
}

// tryStreamProvider dispatches a single provider's streaming call, falling back
// to non-streaming SendMessage (wrapped as a channel) when the provider does not
// implement StreamClient.
func (cc *ChainClient) tryStreamProvider(ctx context.Context, p NamedClient, req Request) (<-chan StreamChunk, error) {
	sc, ok := p.Client.(StreamClient)
	if !ok {
		// Provider does not support streaming; fall back to non-streaming.
		return nonStreamingToChannel(ctx, p.Client, req)
	}
	return sc.StreamMessage(ctx, req)
}

// nonStreamingToChannel wraps a non-streaming Client call into a channel.
func nonStreamingToChannel(ctx context.Context, client Client, req Request) (<-chan StreamChunk, error) {
	resp, err := client.SendMessage(ctx, req)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamChunk, 2)
	if len(resp.ToolCalls) > 0 {
		ch <- StreamChunk{ToolCalls: resp.ToolCalls}
	} else {
		ch <- StreamChunk{Content: resp.Content}
	}
	ch <- StreamChunk{Done: true, Model: resp.Model}
	close(ch)
	return ch, nil
}

// Diagnostics returns per-provider error info from the last SendMessage call.
// Since provider errors are now tracked locally per-call (not on the struct),
// Diagnostics always returns nil. Error details are embedded in the error
// string returned by SendMessage when all providers fail.
func (cc *ChainClient) Diagnostics() []ProviderDiagnostic {
	return nil
}

// IsHTTPError reports whether err is an *HTTPError.
func IsHTTPError(err error) bool { //nolint:revive // exported for callers
	var httpErr *HTTPError
	return errors.As(err, &httpErr)
}
