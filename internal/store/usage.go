package store

import (
	"context"
	"fmt"
	"time"

	"github.com/lgldsilva/semidx/internal/usage"
)

// UsageStore persists and aggregates product search usage events.
type UsageStore interface {
	RecordUsageEvent(ctx context.Context, e usage.Event) error
	UsageAggregate(ctx context.Context, since time.Time, project string, topLimit int) (usage.Aggregate, error)
}

// RecordUsageEvent inserts one usage analytics row. Failures are returned to
// the caller; search paths should not abort on analytics errors.
func (s *PgStore) RecordUsageEvent(ctx context.Context, e usage.Event) error {
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	src := string(usage.ParseSource(string(e.Source)))
	outcome := string(e.Outcome)
	if outcome == "" {
		outcome = string(usage.OutcomeOK)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO usage_events (ts, project, source, outcome, hit_count, latency_ms, keyword, graph, query_hash, query_text)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		e.TS, e.Project, src, outcome, e.HitCount, e.LatencyMS, e.Keyword, e.Graph, e.QueryHash, e.QueryText,
	)
	if err != nil {
		return fmt.Errorf("record usage event: %w", err)
	}
	return nil
}

// UsageAggregate rolls up usage_events since the given timestamp.
func (s *PgStore) UsageAggregate(ctx context.Context, since time.Time, project string, topLimit int) (usage.Aggregate, error) {
	if topLimit <= 0 {
		topLimit = 10
	}
	agg := usage.Aggregate{ProjectsWithEvents: map[string]struct{}{}}

	var total int
	if project == "" {
		err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM usage_events WHERE ts >= $1`, since).Scan(&total)
		if err != nil {
			return agg, fmt.Errorf("usage total: %w", err)
		}
	} else {
		err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM usage_events WHERE ts >= $1 AND project = $2`, since, project).Scan(&total)
		if err != nil {
			return agg, fmt.Errorf("usage total: %w", err)
		}
	}
	agg.Total = total

	byProject, err := s.usageGroup(ctx, since, project, "project", topLimit)
	if err != nil {
		return agg, err
	}
	agg.ByProject = byProject
	for _, c := range byProject {
		agg.ProjectsWithEvents[c.Key] = struct{}{}
	}

	bySource, err := s.usageGroup(ctx, since, project, "source", 0)
	if err != nil {
		return agg, err
	}
	agg.BySource = bySource

	byOutcome, err := s.usageGroup(ctx, since, project, "outcome", 0)
	if err != nil {
		return agg, err
	}
	agg.ByOutcome = byOutcome
	return agg, nil
}

func (s *PgStore) usageGroup(ctx context.Context, since time.Time, project, col string, limit int) ([]usage.Count, error) {
	// col is a closed set of column names from callers — never user input.
	var q string
	var args []any
	switch col {
	case "project", "source", "outcome":
	default:
		return nil, fmt.Errorf("invalid usage group column %q", col)
	}
	if project == "" {
		q = fmt.Sprintf(`SELECT %s, COUNT(*) AS n FROM usage_events WHERE ts >= $1 GROUP BY 1 ORDER BY n DESC`, col)
		args = []any{since}
	} else {
		q = fmt.Sprintf(`SELECT %s, COUNT(*) AS n FROM usage_events WHERE ts >= $1 AND project = $2 GROUP BY 1 ORDER BY n DESC`, col)
		args = []any{since, project}
	}
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, limit)
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("usage group %s: %w", col, err)
	}
	defer rows.Close()
	var out []usage.Count
	for rows.Next() {
		var c usage.Count
		if err := rows.Scan(&c.Key, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
