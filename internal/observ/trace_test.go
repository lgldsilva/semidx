package observ

import (
	"context"
	"testing"
)

func TestStartSpanNoop(t *testing.T) {
	ctx, span := StartSpan(context.Background(), "test")
	if ctx == nil {
		t.Fatal("StartSpan returned nil context")
	}
	if span == nil {
		t.Fatal("StartSpan returned nil span")
	}
	// Noop spans are valid and end without panic.
	span.End()

	s := SpanFromContext(ctx)
	if s == nil {
		t.Fatal("SpanFromContext returned nil span")
	}
	s.End()
}
