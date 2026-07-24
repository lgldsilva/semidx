// Package usage records and reports product-level search analytics (counts by
// project/source/outcome). Inspired by ai-memory's auto-improve-report pattern:
// append-only events, aggregate reports with findings and explicit blind spots.
// Query text is never stored by default.
package usage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// Source is a closed enum of search call origins.
type Source string

const (
	SourceCLI     Source = "cli"
	SourceMCP     Source = "mcp"
	SourceAdmin   Source = "admin"
	SourceSDK     Source = "sdk"
	SourceUnknown Source = "unknown"
)

// Outcome is a closed enum of search results.
type Outcome string

const (
	OutcomeOK       Outcome = "ok"       // ≥1 hits, semantic (not keyword fallback)
	OutcomeEmpty    Outcome = "empty"    // 0 hits
	OutcomeFallback Outcome = "fallback" // keyword fallback or keyword-only with hits
	OutcomeError    Outcome = "error"    // search failed
)

// Event is one recorded search attempt. QueryText is only populated when the
// operator explicitly opts in via LogQueries; otherwise QueryHash may still be
// set for correlation without revealing intent.
type Event struct {
	TS        time.Time
	Project   string
	Source    Source
	Outcome   Outcome
	HitCount  int
	LatencyMS int64
	Keyword   bool
	Graph     bool
	QueryHash string
	QueryText string // opt-in only
}

// Aggregate is the raw store rollup used to build a Report.
type Aggregate struct {
	Total              int
	ByProject          []Count
	BySource           []Count
	ByOutcome          []Count
	ProjectsWithEvents map[string]struct{}
}

// Count is one key→count row in an aggregate table.
type Count struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// Params configures a usage report.
type Params struct {
	SinceDays int
	TopLimit  int
	Project   string // optional filter
}

// DefaultParams returns the report defaults (30d, top 10).
func DefaultParams() Params {
	return Params{SinceDays: 30, TopLimit: 10}
}

// Report mirrors ai-memory's telemetry report shape: summary, aggregates,
// findings, and known blind spots.
type Report struct {
	GeneratedAt string    `json:"generated_at"`
	SinceDays   int       `json:"since_days"`
	Project     string    `json:"project,omitempty"`
	Summary     string    `json:"summary"`
	Total       int       `json:"total"`
	ByProject   []Count   `json:"by_project"`
	BySource    []Count   `json:"by_source"`
	ByOutcome   []Count   `json:"by_outcome"`
	Rates       Rates     `json:"rates"`
	Findings    []Finding `json:"findings"`
	BlindSpots  []string  `json:"blind_spots"`
}

// Rates are outcome fractions over Total (0 when Total is 0).
type Rates struct {
	OK       float64 `json:"ok"`
	Empty    float64 `json:"empty"`
	Fallback float64 `json:"fallback"`
	Error    float64 `json:"error"`
	MCP      float64 `json:"mcp"`
	CLI      float64 `json:"cli"`
}

// Finding is a bounded operational signal.
type Finding struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// ParseSource validates a client-origin string into a Source enum.
func ParseSource(s string) Source {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case string(SourceCLI):
		return SourceCLI
	case string(SourceMCP):
		return SourceMCP
	case string(SourceAdmin):
		return SourceAdmin
	case string(SourceSDK):
		return SourceSDK
	default:
		return SourceUnknown
	}
}

// HashQuery returns a stable SHA-256 hex of the query (empty → "").
func HashQuery(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(q))
	return hex.EncodeToString(sum[:])
}

// Classify maps a successful search response into an Outcome.
func Classify(hitCount int, fallback, keywordOnly bool) Outcome {
	if hitCount == 0 {
		return OutcomeEmpty
	}
	if fallback || keywordOnly {
		return OutcomeFallback
	}
	return OutcomeOK
}

type ctxKey struct{}

// WithSource annotates ctx with the search call origin.
func WithSource(ctx context.Context, src Source) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ctxKey{}, src)
}

// SourceFrom returns the Source stored in ctx, or SourceUnknown.
func SourceFrom(ctx context.Context) Source {
	if ctx == nil {
		return SourceUnknown
	}
	if s, ok := ctx.Value(ctxKey{}).(Source); ok && s != "" {
		return s
	}
	return SourceUnknown
}

// Recorder receives one usage event. Implementations must be safe for concurrent use.
// A nil Recorder is a no-op (see Nop).
type Recorder interface {
	Record(ctx context.Context, e Event)
}

// Nop is a Recorder that discards events.
type Nop struct{}

// Record implements Recorder.
func (Nop) Record(context.Context, Event) {}

// StoreWriter persists usage events.
type StoreWriter interface {
	RecordUsageEvent(ctx context.Context, e Event) error
}

// StoreRecorder writes events via StoreWriter. Failures are swallowed by the
// caller (search must not fail because analytics failed); Record logs nothing —
// the wiring site may wrap with slog if desired.
type StoreRecorder struct {
	Store      StoreWriter
	LogQueries bool // when true, persist QueryText (still hashed)
}

// Record implements Recorder.
func (r *StoreRecorder) Record(ctx context.Context, e Event) {
	if r == nil || r.Store == nil {
		return
	}
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	e.Source = ParseSource(string(e.Source))
	if e.QueryHash == "" && e.QueryText != "" {
		e.QueryHash = HashQuery(e.QueryText)
	}
	if !r.LogQueries {
		e.QueryText = ""
	}
	_ = r.Store.RecordUsageEvent(ctx, e)
}
