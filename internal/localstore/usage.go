package localstore

import (
	"context"
	"fmt"
	"time"

	"github.com/lgldsilva/semidx/internal/usage"
)

// RecordUsageEvent inserts one usage analytics row into the local SQLite index.
func (s *SQLiteStore) RecordUsageEvent(ctx context.Context, e usage.Event) error {
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	src := string(usage.ParseSource(string(e.Source)))
	outcome := string(e.Outcome)
	if outcome == "" {
		outcome = string(usage.OutcomeOK)
	}
	kw, graph := 0, 0
	if e.Keyword {
		kw = 1
	}
	if e.Graph {
		graph = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO usage_events (ts, project, source, outcome, hit_count, latency_ms, keyword, graph, query_hash, query_text)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.TS.UTC().Format(time.RFC3339Nano), e.Project, src, outcome, e.HitCount, e.LatencyMS, kw, graph, e.QueryHash, e.QueryText,
	)
	if err != nil {
		return fmt.Errorf("record usage event: %w", err)
	}
	return nil
}

// UsageAggregate rolls up local usage_events since the given timestamp.
func (s *SQLiteStore) UsageAggregate(ctx context.Context, since time.Time, project string, topLimit int) (usage.Aggregate, error) {
	if topLimit <= 0 {
		topLimit = 10
	}
	agg := usage.Aggregate{ProjectsWithEvents: map[string]struct{}{}}
	sinceStr := since.UTC().Format(time.RFC3339Nano)

	var total int
	var err error
	if project == "" {
		err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_events WHERE ts >= ?`, sinceStr).Scan(&total)
	} else {
		err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_events WHERE ts >= ? AND project = ?`, sinceStr, project).Scan(&total)
	}
	if err != nil {
		return agg, fmt.Errorf("usage total: %w", err)
	}
	agg.Total = total

	byProject, err := s.usageGroup(ctx, sinceStr, project, "project", topLimit)
	if err != nil {
		return agg, err
	}
	agg.ByProject = byProject
	for _, c := range byProject {
		agg.ProjectsWithEvents[c.Key] = struct{}{}
	}
	bySource, err := s.usageGroup(ctx, sinceStr, project, "source", 0)
	if err != nil {
		return agg, err
	}
	agg.BySource = bySource
	byOutcome, err := s.usageGroup(ctx, sinceStr, project, "outcome", 0)
	if err != nil {
		return agg, err
	}
	agg.ByOutcome = byOutcome
	return agg, nil
}

func (s *SQLiteStore) usageGroup(ctx context.Context, since, project, col string, limit int) ([]usage.Count, error) {
	switch col {
	case "project", "source", "outcome":
	default:
		return nil, fmt.Errorf("invalid usage group column %q", col)
	}
	var q string
	var args []any
	if project == "" {
		q = fmt.Sprintf(`SELECT %s, COUNT(*) AS n FROM usage_events WHERE ts >= ? GROUP BY 1 ORDER BY n DESC`, col)
		args = []any{since}
	} else {
		q = fmt.Sprintf(`SELECT %s, COUNT(*) AS n FROM usage_events WHERE ts >= ? AND project = ? GROUP BY 1 ORDER BY n DESC`, col)
		args = []any{since, project}
	}
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("usage group %s: %w", col, err)
	}
	defer func() { _ = rows.Close() }()
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
