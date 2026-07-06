// Package observ provides lightweight observability helpers (tracing, metrics)
// used across semidx. By default, OpenTelemetry is wired to the global tracer
// provider (noop when no provider is configured), so there is no runtime cost
// when tracing is not enabled.
package observ

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("github.com/lgldsilva/semidx")

// StartSpan starts a new span with the given name and returns the context
// carrying the span. Callers must call span.End() when done.
func StartSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	return tracer.Start(ctx, name)
}

// SpanFromContext returns the current span from ctx, or a noop span if none.
func SpanFromContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}
