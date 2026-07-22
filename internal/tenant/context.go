// Package tenant carries the authenticated tenant boundary through semidx
// request and indexing contexts. The context is deliberately small: stores
// remain compatible with the standalone backend while server requests gain an
// explicit tenant identity.
package tenant

import (
	"context"
	"errors"
)

// DefaultID is the compatibility tenant used by existing single-tenant
// installations and standalone indexes.
const DefaultID = 1

// Context identifies the tenant and principal authorized for an operation.
type Context struct {
	ID          int
	Slug        string
	WorkspaceID int
	Workspace   string
	UserID      int
	Role        string
	GlobalAdmin bool
}

type contextKey struct{}

// With attaches a tenant boundary to ctx. Invalid tenant IDs are rejected so
// callers cannot accidentally create an unscoped request.
func With(ctx context.Context, tc Context) (context.Context, error) {
	if tc.ID <= 0 {
		return nil, errors.New("tenant: id must be positive")
	}
	return context.WithValue(ctx, contextKey{}, tc), nil
}

// MustWith is the convenient form for trusted internal call sites. It is only
// intended for constants and already-validated tenant IDs.
func MustWith(ctx context.Context, tc Context) context.Context {
	out, err := With(ctx, tc)
	if err != nil {
		panic(err)
	}
	return out
}

// From returns the tenant context when one was attached.
func From(ctx context.Context) (Context, bool) {
	tc, ok := ctx.Value(contextKey{}).(Context)
	return tc, ok && tc.ID > 0
}

// ID returns the active tenant ID. The default is intentionally retained for
// local/indexer callers and backward-compatible single-tenant API operations.
func ID(ctx context.Context) int {
	if tc, ok := From(ctx); ok {
		return tc.ID
	}
	return DefaultID
}

// Require returns an explicit tenant context and rejects legacy unscoped
// contexts at boundaries that require SaaS isolation.
func Require(ctx context.Context) (Context, error) {
	tc, ok := From(ctx)
	if !ok {
		return Context{}, errors.New("tenant: request has no tenant context")
	}
	return tc, nil
}
