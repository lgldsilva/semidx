package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/searchtargets"
	"github.com/lgldsilva/semidx/internal/xdg"
)

// Insight holds the trend data for a tracked query.
type Insight struct {
	Project    string      `json:"project"`
	Name       string      `json:"name"`
	Query      string      `json:"query"`
	Datapoints []Datapoint `json:"datapoints"`
}

// Datapoint is one measurement point.
type Datapoint struct {
	Timestamp  time.Time `json:"timestamp"`
	MatchCount int       `json:"match_count"`
}

// insightsFile returns the path to the insights JSON file.
func insightsFile() (string, error) {
	dir, err := xdg.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "semidx", "insights.json"), nil
}

// loadInsights reads all insights from disk.
func loadInsights() ([]Insight, error) {
	p, err := insightsFile()
	if err != nil {
		return nil, err
	}
	// #nosec G304 -- p points to a safe location inside config directory
	data, err := os.ReadFile(filepath.Clean(p))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var insights []Insight
	if err := json.Unmarshal(data, &insights); err != nil {
		return nil, fmt.Errorf("parse insights file: %w", err)
	}
	return insights, nil
}

// saveInsights writes all insights to disk.
func saveInsights(insights []Insight) error {
	p, err := insightsFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(insights, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}

func newInsightsCmd(d *deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "insights",
		Short: "Track code metrics and migration progress over time",
		Long: `Track how code metrics change over time (e.g. migration progress).
Create an insight with a search query, then periodically record the match count.
Use "semidx insights show" to view the trend as an ASCII chart.`,
		Example: `  semidx insights create "go-any-migration" --query "interface{}" --project myapp
  semidx insights record go-any-migration
  semidx insights show go-any-migration`,
	}
	cmd.AddCommand(newInsightsCreateCmd(d))
	cmd.AddCommand(newInsightsRecordCmd(d))
	cmd.AddCommand(newInsightsShowCmd(d))
	return cmd
}

func newInsightsCreateCmd(d *deps) *cobra.Command {
	var project, query string
	c := &cobra.Command{
		Use:     "create <name>",
		Short:   "Create a new insight tracker",
		Args:    cobra.ExactArgs(1),
		Example: `  semidx insights create "go-any-migration" --query "interface{}" --project myapp`,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if query == "" {
				return fmt.Errorf("--query is required")
			}
			insights, err := loadInsights()
			if err != nil {
				return err
			}
			for _, in := range insights {
				if in.Name == name {
					return fmt.Errorf("insight %q already exists", name)
				}
			}
			insights = append(insights, Insight{
				Project: project,
				Name:    name,
				Query:   query,
			})
			if err := saveInsights(insights); err != nil {
				return err
			}
			fmt.Printf("Insight %q created (project: %s, query: %s)\n", name, project, query)
			return nil
		},
	}
	c.Flags().StringVar(&project, "project", "", "Project path or name")
	c.Flags().StringVar(&query, "query", "", "Search query")
	return c
}

func newInsightsRecordCmd(d *deps) *cobra.Command {
	return &cobra.Command{
		Use:     "record <name>",
		Short:   "Record the current match count for an insight",
		Args:    cobra.ExactArgs(1),
		Example: `  semidx insights record go-any-migration`,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			insights, err := loadInsights()
			if err != nil {
				return err
			}
			idx := -1
			for i, in := range insights {
				if in.Name == name {
					idx = i
					break
				}
			}
			if idx < 0 {
				return fmt.Errorf("insight %q not found", name)
			}
			ins := &insights[idx]

			// Count matches by running the search.
			count, err := countMatches(cmd, d, ins.Project, ins.Query)
			if err != nil {
				return fmt.Errorf("record insight %q: %w", name, err)
			}

			ins.Datapoints = append(ins.Datapoints, Datapoint{
				Timestamp:  time.Now(),
				MatchCount: count,
			})
			if err := saveInsights(insights); err != nil {
				return err
			}
			fmt.Printf("Recorded %d matches for insight %q.\n", count, name)
			return nil
		},
	}
}

func newInsightsShowCmd(_ *deps) *cobra.Command {
	return &cobra.Command{
		Use:     "show <name>",
		Short:   "Show the trend chart for an insight",
		Args:    cobra.ExactArgs(1),
		Example: `  semidx insights show go-any-migration`,
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			insights, err := loadInsights()
			if err != nil {
				return err
			}
			var ins *Insight
			for i, in := range insights {
				if in.Name == name {
					ins = &insights[i]
					break
				}
			}
			if ins == nil {
				return fmt.Errorf("insight %q not found", name)
			}
			if len(ins.Datapoints) == 0 {
				fmt.Printf("Insight %q: no data yet. Run `semidx insights record %s`.\n", name, name)
				return nil
			}
			printInsightChart(ins)
			return nil
		},
	}
}

// countMatches runs the search query and returns the total number of matches.
func countMatches(cmd *cobra.Command, d *deps, project, query string) (int, error) {
	ctx := cmd.Context()
	var count int
	if d.remote() {
		api := d.apiClient()
		p, err := searchtargets.ResolveRemoteProject(ctx, api, project)
		if err != nil {
			return 0, err
		}
		sr, err := api.Search(ctx, p.Name, query, "", 500, false, 0)
		if err != nil {
			return 0, err
		}
		return len(sr.Results), nil
	}
	db, err := d.indexStore(ctx)
	if err != nil {
		return 0, err
	}
	targets, err := searchtargets.ResolveProjects(ctx, db, project, "")
	if err != nil || len(targets) == 0 {
		return 0, fmt.Errorf("resolve project: %w", err)
	}
	req := search.Request{Query: query, TopK: 500, KeywordOnly: d.keywordOnly}
	results, err := searchtargets.SearchLocal(ctx, db, d.emb, targets, req, gitmeta.Info{})
	if err != nil {
		return 0, err
	}
	for _, r := range results {
		count += len(r.Resp.Results)
	}
	return count, nil
}

// printInsightChart renders an ASCII bar chart of the insight's datapoints.
func printInsightChart(ins *Insight) {
	dps := ins.Datapoints
	// Sort by timestamp.
	sort.Slice(dps, func(i, j int) bool {
		return dps[i].Timestamp.Before(dps[j].Timestamp)
	})

	// Header.
	title := fmt.Sprintf("%s: %s", ins.Name, ins.Query)
	fmt.Println(title)
	fmt.Println(strings.Repeat("─", len(title)))

	if len(dps) == 0 {
		return
	}

	// Determine max count for scaling.
	maxCount := 0
	for _, dp := range dps {
		if dp.MatchCount > maxCount {
			maxCount = dp.MatchCount
		}
	}
	if maxCount == 0 {
		maxCount = 1
	}

	// Scale: chart width is ~50 chars.
	const chartWidth = 50
	scale := float64(chartWidth) / float64(maxCount)

	// Group datapoints by month for display.
	type monthKey struct {
		year  int
		month time.Month
	}
	monthGroups := make(map[monthKey][]Datapoint)
	var sortedMonths []monthKey
	monthSeen := make(map[monthKey]bool)

	for _, dp := range dps {
		mk := monthKey{dp.Timestamp.Year(), dp.Timestamp.Month()}
		monthGroups[mk] = append(monthGroups[mk], dp)
		if !monthSeen[mk] {
			monthSeen[mk] = true
			sortedMonths = append(sortedMonths, mk)
		}
	}
	// Sort months chronologically.
	sort.Slice(sortedMonths, func(i, j int) bool {
		if sortedMonths[i].year != sortedMonths[j].year {
			return sortedMonths[i].year < sortedMonths[j].year
		}
		return sortedMonths[i].month < sortedMonths[j].month
	})

	// Bar chart labels.
	monthAbbrev := map[time.Month]string{
		time.January: "Jan", time.February: "Feb", time.March: "Mar",
		time.April: "Apr", time.May: "May", time.June: "Jun",
		time.July: "Jul", time.August: "Aug", time.September: "Sep",
		time.October: "Oct", time.November: "Nov", time.December: "Dec",
	}

	for _, mk := range sortedMonths {
		group := monthGroups[mk]
		// Average the counts in this month.
		total := 0
		for _, dp := range group {
			total += dp.MatchCount
		}
		avgCount := float64(total) / float64(len(group))
		barLen := int(math.Round(avgCount * scale))
		if barLen < 1 && avgCount > 0 {
			barLen = 1
		}
		bar := strings.Repeat("█", barLen)
		label := fmt.Sprintf("%s %02d", monthAbbrev[mk.month], mk.year%100)
		fmt.Printf("%s %s %d\n", label, bar, int(math.Round(avgCount)))
	}

	// Show trend info.
	if len(dps) >= 2 {
		first, last := dps[0].MatchCount, dps[len(dps)-1].MatchCount
		diff := last - first
		sign := ""
		if diff > 0 {
			sign = "+"
		}
		pct := 0.0
		if first > 0 {
			pct = float64(diff) / float64(first) * 100
		}
		duration := "N/A"
		if !dps[0].Timestamp.IsZero() && !dps[len(dps)-1].Timestamp.IsZero() {
			dur := dps[len(dps)-1].Timestamp.Sub(dps[0].Timestamp)
			months := int(dur.Hours() / (24 * 30))
			if months < 1 {
				months = 1
			}
			duration = fmt.Sprintf("%d months", months)
		}
		fmt.Printf("\n%s %s%d (%s%.1f%%) in %s\n",
			arrowSymbol(diff), sign, diff, sign, pct, duration)
	}
}

// arrowSymbol returns a trend arrow based on the diff.
func arrowSymbol(diff int) string {
	if diff < 0 {
		return "↓"
	} else if diff > 0 {
		return "↑"
	}
	return "→"
}
