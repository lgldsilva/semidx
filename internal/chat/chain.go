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
		if cc.OnFallback != nil {
			cc.OnFallback(p.Name, err)
		}

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

	// Build an actionable summary of all provider errors, keeping %w on the
	// last error so callers can use errors.As to inspect HTTPError etc.
	var b strings.Builder
	b.WriteString("chain: all chat providers failed.\n")
	for _, d := range providerErrors {
		fmt.Fprintf(&b, "- %s: %s\n", d.Name, d.Error)
	}
	b.WriteString("Check your API keys: GEMINI_API_KEY and OPENROUTER_API_KEY.")
	return nil, fmt.Errorf("%s (%w)", strings.TrimSpace(b.String()), lastErr)
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

		sc, ok := p.Client.(StreamClient)
		if !ok {
			// Provider does not support streaming; fall back to non-streaming.
			chunks, err := nonStreamingToChannel(ctx, p.Client, req)
			if err != nil {
				lastErr = err
				if cc.OnFallback != nil {
					cc.OnFallback(p.Name, err)
				}
				continue
			}
			return chunks, nil
		}

		chunks, err := sc.StreamMessage(ctx, req)
		if err != nil {
			lastErr = err
			if cc.OnFallback != nil {
				cc.OnFallback(p.Name, err)
			}
			continue
		}
		return chunks, nil
	}

	return nil, fmt.Errorf("chain: all streaming providers failed (%w)", lastErr)
}

// nonStreamingToChannel wraps a non-streaming Client call into a channel.
func nonStreamingToChannel(ctx context.Context, client Client, req Request) (<-chan StreamChunk, error) {
	resp, err := client.SendMessage(ctx, req)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamChunk, 2)
	ch <- StreamChunk{Content: resp.Content}
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
