package usage

import (
	"fmt"
	"strings"
	"time"
)

// BuildReport turns a store Aggregate into a Report with rates, findings, and
// blind spots (ai-memory auto-improve-report mold).
func BuildReport(agg Aggregate, params Params, generatedAt time.Time) Report {
	if params.SinceDays <= 0 {
		params.SinceDays = 30
	}
	if params.TopLimit <= 0 {
		params.TopLimit = 10
	}
	if generatedAt.IsZero() {
		generatedAt = time.Now().UTC()
	}

	byProject := truncateCounts(agg.ByProject, params.TopLimit)
	bySource := agg.BySource
	byOutcome := agg.ByOutcome

	rates := Rates{
		OK:       rate(countKey(byOutcome, string(OutcomeOK)), agg.Total),
		Empty:    rate(countKey(byOutcome, string(OutcomeEmpty)), agg.Total),
		Fallback: rate(countKey(byOutcome, string(OutcomeFallback)), agg.Total),
		Error:    rate(countKey(byOutcome, string(OutcomeError)), agg.Total),
		MCP:      rate(countKey(bySource, string(SourceMCP)), agg.Total),
		CLI:      rate(countKey(bySource, string(SourceCLI)), agg.Total),
	}

	summary := fmt.Sprintf("%d search(es) in the last %d day(s).", agg.Total, params.SinceDays)
	if agg.Total > 0 {
		summary = fmt.Sprintf(
			"%d search(es) in the last %d day(s); %.0f%% ok, %.0f%% empty, %.0f%% fallback, %.0f%% error; %.0f%% mcp / %.0f%% cli.",
			agg.Total, params.SinceDays,
			rates.OK*100, rates.Empty*100, rates.Fallback*100, rates.Error*100,
			rates.MCP*100, rates.CLI*100,
		)
	}

	findings := make([]Finding, 0, 4)
	if agg.Total == 0 {
		findings = append(findings, Finding{
			Kind:     "no_searches",
			Severity: "info",
			Message:  "No search events in the selected window.",
		})
	}
	if agg.Total > 0 && rates.Empty >= 0.5 {
		findings = append(findings, Finding{
			Kind:     "high_empty_rate",
			Severity: "warning",
			Message:  fmt.Sprintf("%.0f%% of searches returned zero hits — check index freshness or query phrasing.", rates.Empty*100),
		})
	}
	if agg.Total > 0 && rates.Fallback >= 0.4 {
		findings = append(findings, Finding{
			Kind:     "high_fallback_rate",
			Severity: "warning",
			Message:  fmt.Sprintf("%.0f%% of searches used keyword fallback — embedding provider may be down or keyword-only mode is active.", rates.Fallback*100),
		})
	}
	if agg.Total > 0 && rates.Error >= 0.1 {
		findings = append(findings, Finding{
			Kind:     "elevated_error_rate",
			Severity: "warning",
			Message:  fmt.Sprintf("%.0f%% of searches failed.", rates.Error*100),
		})
	}
	if agg.Total > 0 && rates.MCP == 0 && rates.CLI == 0 {
		findings = append(findings, Finding{
			Kind:     "unknown_sources_only",
			Severity: "info",
			Message:  "No cli/mcp-tagged events — clients may predate X-Semidx-Client, or only admin/sdk traffic was recorded.",
		})
	}

	return Report{
		GeneratedAt: generatedAt.UTC().Format(time.RFC3339),
		SinceDays:   params.SinceDays,
		Project:     params.Project,
		Summary:     summary,
		Total:       agg.Total,
		ByProject:   byProject,
		BySource:    bySource,
		ByOutcome:   byOutcome,
		Rates:       rates,
		Findings:    findings,
		BlindSpots: []string{
			"Events recorded before this feature shipped are absent.",
			"Query text is not stored by default (only optional SEMIDX_USAGE_LOG_QUERIES).",
			"Remote clients without X-Semidx-Client appear as source=unknown.",
			"Prometheus histograms (semidx_search_duration_seconds) only count successful HTTP searches and predate source/outcome labels.",
			"Skill and mcp-install adoption is not inferred from search events — use semidx doctor.",
		},
	}
}

// FormatText renders a human-readable usage report for the CLI.
func FormatText(r Report) string {
	var b strings.Builder
	b.WriteString("# semidx usage\n\n")
	fmt.Fprintf(&b, "- Generated: %s\n", r.GeneratedAt)
	fmt.Fprintf(&b, "- Window: last %d day(s)\n", r.SinceDays)
	if r.Project != "" {
		fmt.Fprintf(&b, "- Project filter: %s\n", r.Project)
	}
	fmt.Fprintf(&b, "- Summary: %s\n\n", r.Summary)

	b.WriteString("## By project\n\n")
	writeCounts(&b, r.ByProject)
	b.WriteString("\n## By source\n\n")
	writeCounts(&b, r.BySource)
	b.WriteString("\n## By outcome\n\n")
	writeCounts(&b, r.ByOutcome)

	b.WriteString("\n## Findings\n\n")
	if len(r.Findings) == 0 {
		b.WriteString("None.\n")
	} else {
		for _, f := range r.Findings {
			fmt.Fprintf(&b, "- **%s** (%s) — %s\n", f.Kind, f.Severity, f.Message)
		}
	}
	b.WriteString("\n## Blind spots\n\n")
	for _, s := range r.BlindSpots {
		fmt.Fprintf(&b, "- %s\n", s)
	}
	return b.String()
}

func writeCounts(b *strings.Builder, rows []Count) {
	if len(rows) == 0 {
		b.WriteString("None.\n")
		return
	}
	for _, row := range rows {
		fmt.Fprintf(b, "- `%s`: %d\n", row.Key, row.Count)
	}
}

func countKey(rows []Count, key string) int {
	for _, r := range rows {
		if r.Key == key {
			return r.Count
		}
	}
	return 0
}

func rate(n, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(n) / float64(total)
}

func truncateCounts(rows []Count, limit int) []Count {
	if limit <= 0 || len(rows) <= limit {
		return rows
	}
	return rows[:limit]
}
