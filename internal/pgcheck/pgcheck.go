// Package pgcheck inspects a PostgreSQL database for the capabilities semidx
// needs — the pgvector and pg_trgm extensions, and the halfvec operator class
// used to index embeddings above pgvector's 2000-dimension HNSW limit. It is a
// read-only probe: unlike opening a store, it never creates anything, so
// `semidx doctor` can report on a database it must not modify.
package pgcheck

import (
	"context"
	"fmt"
)

// Queryer is the subset of a pgx pool/connection pgcheck needs, so the probe can
// run against a real database or a fake in tests.
type Queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) Row
}

// Row is the scan half of a query result.
type Row interface {
	Scan(dest ...any) error
}

// Extension is the state of one PostgreSQL extension.
type Extension struct {
	Name      string
	Installed bool   // present in pg_extension (CREATE EXTENSION has run)
	Available bool   // present in pg_available_extensions (installable here)
	Version   string // extversion when installed
}

// Report is what a probe found. A field left at its zero value means the probe
// could not determine it.
type Report struct {
	ServerVersion  string
	Vector         Extension
	Trgm           Extension
	HalfvecOpclass bool // halfvec_cosine_ops exists (pgvector >= 0.7)
	CanCreate      bool // the connected role may run CREATE EXTENSION
}

// requiredExtensions are created by the migrations; the database role must be
// able to create them, or an operator must create them ahead of time.
var requiredExtensions = []string{"vector", "pg_trgm"}

// Inspect probes q and returns what it could learn. It returns an error only
// when the database is unreachable; a missing extension is a finding, not an
// error.
func Inspect(ctx context.Context, q Queryer) (*Report, error) {
	r := &Report{}
	if err := q.QueryRow(ctx, "SELECT current_setting('server_version')").Scan(&r.ServerVersion); err != nil {
		return nil, fmt.Errorf("query server version: %w", err)
	}

	exts := map[string]*Extension{"vector": &r.Vector, "pg_trgm": &r.Trgm}
	for _, name := range requiredExtensions {
		ext := exts[name]
		ext.Name = name
		var version *string
		if err := q.QueryRow(ctx,
			"SELECT extversion FROM pg_extension WHERE extname = $1", name).Scan(&version); err == nil && version != nil {
			ext.Installed, ext.Version = true, *version
		}
		if !ext.Installed {
			var available bool
			if err := q.QueryRow(ctx,
				"SELECT EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = $1)", name).Scan(&available); err == nil {
				ext.Available = available
			}
			continue
		}
		ext.Available = true
	}

	// Probe the capability rather than parsing the version: halfvec_cosine_ops
	// is exactly what EnsureChunksTable needs for models above 2000 dimensions.
	_ = q.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_opclass WHERE opcname = 'halfvec_cosine_ops')").Scan(&r.HalfvecOpclass)
	_ = q.QueryRow(ctx,
		"SELECT COALESCE(rolsuper, false) FROM pg_roles WHERE rolname = current_user").Scan(&r.CanCreate)
	return r, nil
}

// Findings returns the problems worth acting on, most blocking first. An empty
// slice means the database is ready for semidx.
func (r *Report) Findings() []string {
	var out []string
	for _, ext := range []Extension{r.Vector, r.Trgm} {
		switch {
		case ext.Installed:
		case ext.Available && r.CanCreate:
			out = append(out, fmt.Sprintf("extension %q is not created yet — migrations will run `CREATE EXTENSION %s`", ext.Name, ext.Name))
		case ext.Available:
			out = append(out, fmt.Sprintf("extension %q is not created and the current role may not create it — ask an admin for `CREATE EXTENSION %s`", ext.Name, ext.Name))
		default:
			out = append(out, fmt.Sprintf("extension %q is NOT available on this server — semidx cannot run here (use the pgvector/pgvector image, see `semidx docker`)", ext.Name))
		}
	}
	if r.Vector.Installed && !r.HalfvecOpclass {
		out = append(out, fmt.Sprintf("pgvector %s has no halfvec_cosine_ops: models above 2000 dimensions (e.g. Gemini 3072) cannot be indexed — upgrade pgvector to 0.7 or newer", r.Vector.Version))
	}
	return out
}
