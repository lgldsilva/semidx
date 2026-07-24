package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/internal/usage"
)

func newUsageCmd(d *deps) *cobra.Command {
	var (
		days    int
		project string
		asJSON  bool
	)
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Show search usage analytics (counts by project, source, outcome)",
		Long: `Report product usage for semantic search: how many times each project was
queried, whether calls came from MCP/CLI/admin, and ok/empty/fallback/error rates.

Modeled on ai-memory's auto-improve-report: aggregates + findings + blind spots.
Query text is not stored by default (see SEMIDX_USAGE_LOG_QUERIES).`,
		Example: `  semidx usage
  semidx usage --days 7 --json
  semidx usage --project jackui`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if days <= 0 || days > 365 {
				return fmt.Errorf("--days must be 1..365")
			}
			params := usage.Params{SinceDays: days, TopLimit: 10, Project: strings.TrimSpace(project)}
			report, err := d.loadUsageReport(cmd.Context(), params)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), usage.FormatText(report))
			return err
		},
	}
	cmd.Flags().IntVar(&days, "days", 30, "lookback window in days")
	cmd.Flags().StringVar(&project, "project", "", "optional project name filter")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit machine-readable JSON")
	return cmd
}

func (d *deps) loadUsageReport(ctx context.Context, params usage.Params) (usage.Report, error) {
	if d.remote() {
		raw, err := d.apiClient().SearchUsage(ctx, params.SinceDays, params.Project)
		if err != nil {
			return usage.Report{}, err
		}
		b, err := json.Marshal(raw)
		if err != nil {
			return usage.Report{}, err
		}
		var report usage.Report
		if err := json.Unmarshal(b, &report); err != nil {
			return usage.Report{}, err
		}
		return report, nil
	}
	db, err := d.indexStore(ctx)
	if err != nil {
		return usage.Report{}, err
	}
	us, ok := db.(store.UsageStore)
	if !ok {
		return usage.Report{}, fmt.Errorf("active backend does not support usage analytics")
	}
	since := time.Now().UTC().AddDate(0, 0, -params.SinceDays)
	agg, err := us.UsageAggregate(ctx, since, params.Project, params.TopLimit)
	if err != nil {
		return usage.Report{}, err
	}
	return usage.BuildReport(agg, params, time.Now().UTC()), nil
}
