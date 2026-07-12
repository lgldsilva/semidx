package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/searchtargets"
	"github.com/lgldsilva/semidx/internal/xdg"
	"github.com/lgldsilva/semidx/pkg/client"
)

// Alert represents a saved search alert.
type Alert struct {
	Project  string `json:"project"`
	Name     string `json:"name"`
	Query    string `json:"query"`
	LastHash string `json:"last_hash"`
}

// alertsFile returns the path to the alerts JSON file.
func alertsFile() (string, error) {
	dir, err := xdg.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "semidx", "alerts.json"), nil
}

// loadAlerts reads all alerts from disk.
func loadAlerts() ([]Alert, error) {
	p, err := alertsFile()
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
	var alerts []Alert
	if err := json.Unmarshal(data, &alerts); err != nil {
		return nil, fmt.Errorf("parse alerts file: %w", err)
	}
	return alerts, nil
}

// saveAlerts writes all alerts to disk.
func saveAlerts(alerts []Alert) error {
	p, err := alertsFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(alerts, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}

// resultsHash produces a deterministic hash of search results for change detection.
func resultsHash(results []search.Response) string {
	h := sha256.New()
	// Sort paths for deterministic ordering.
	paths := make([]string, 0, len(results))
	for _, r := range results {
		for _, res := range r.Results {
			paths = append(paths, fmt.Sprintf("%s:%d-%d", res.FilePath, res.StartLine, res.EndLine))
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func newAlertsCmd(d *deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alerts",
		Short: "Manage saved search alerts (notify on new code matches)",
		Long: `Save searches and get notified when new code matches them.
Alerts are stored in ~/.config/semidx/alerts.json.
Run "semidx alerts check" manually or add it to your CI/cron.`,
		Example: `  semidx alerts create "deprecated-lib" --query "import.*old-lib" --project myapp
  semidx alerts list --project myapp
  semidx alerts delete deprecated-lib
  semidx alerts check --project myapp`,
	}
	cmd.AddCommand(newAlertsCreateCmd(d))
	cmd.AddCommand(newAlertsListCmd(d))
	cmd.AddCommand(newAlertsDeleteCmd(d))
	cmd.AddCommand(newAlertsCheckCmd(d))
	return cmd
}

func newAlertsCreateCmd(d *deps) *cobra.Command {
	var project, query string
	c := &cobra.Command{
		Use:     "create <name>",
		Short:   "Create a new search alert",
		Long:    "Create a named alert that stores a query and project scope for future checks.",
		Args:    cobra.ExactArgs(1),
		Example: `  semidx alerts create "deprecated-lib" --query "import.*old-lib" --project myapp`,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if query == "" {
				return fmt.Errorf("--query is required")
			}
			alerts, err := loadAlerts()
			if err != nil {
				return err
			}
			for _, a := range alerts {
				if a.Name == name {
					return fmt.Errorf("alert %q already exists", name)
				}
			}
			alerts = append(alerts, Alert{
				Project: project,
				Name:    name,
				Query:   query,
			})
			if err := saveAlerts(alerts); err != nil {
				return err
			}
			fmt.Printf("Alert %q created (project: %s, query: %s)\n", name, project, query)
			return nil
		},
	}
	c.Flags().StringVar(&project, "project", "", "Project path or name")
	c.Flags().StringVar(&query, "query", "", "Search query")
	return c
}

func newAlertsListCmd(d *deps) *cobra.Command {
	var project string
	c := &cobra.Command{
		Use:   "list",
		Short: "List saved alerts",
		Long:  "List saved alerts, optionally filtered by project.",
		Example: `  semidx alerts list
  semidx alerts list --project myapp`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runAlertsList(project)
		},
	}
	c.Flags().StringVar(&project, "project", "", "Filter by project")
	return c
}

func runAlertsList(project string) error {
	alerts, err := loadAlerts()
	if err != nil {
		return err
	}
	if project != "" {
		alerts = filterAlertsByProject(alerts, project)
	}
	if len(alerts) == 0 {
		fmt.Println("No alerts found.")
		return nil
	}
	fmt.Println("Saved alerts:")
	for _, a := range alerts {
		status := "new"
		if a.LastHash != "" {
			status = "checked"
		}
		fmt.Printf("  %s (project: %s, query: %q) [%s]\n", a.Name, a.Project, a.Query, status)
	}
	return nil
}

func newAlertsDeleteCmd(_ *deps) *cobra.Command {
	var confirm bool
	c := &cobra.Command{
		Use:     "delete <name>",
		Short:   "Delete an alert",
		Long:    "Delete a saved alert by name. This is destructive and requires --confirm.",
		Args:    cobra.ExactArgs(1),
		Example: `  semidx alerts delete deprecated-lib --confirm`,
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			if !confirm {
				return fmt.Errorf("alerts delete is destructive; re-run with --confirm")
			}
			alerts, err := loadAlerts()
			if err != nil {
				return err
			}
			found := false
			var kept []Alert
			for _, a := range alerts {
				if a.Name == name {
					found = true
					continue
				}
				kept = append(kept, a)
			}
			if !found {
				return fmt.Errorf("alert %q not found", name)
			}
			if err := saveAlerts(kept); err != nil {
				return err
			}
			fmt.Printf("Alert %q deleted.\n", name)
			return nil
		},
	}
	c.Flags().BoolVar(&confirm, "confirm", false, "Confirm alert deletion")
	return c
}

func newAlertsCheckCmd(d *deps) *cobra.Command {
	var project string
	c := &cobra.Command{
		Use:   "check",
		Short: "Check alerts against the current index and report new matches",
		Long:  "Run each alert query and report only new matches since the last check.",
		Example: `  semidx alerts check
  semidx alerts check --project myapp`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			allAlerts, err := loadAlerts()
			if err != nil {
				return err
			}
			toCheck := filterAlertsByProject(allAlerts, project)
			if len(toCheck) == 0 {
				fmt.Println("No alerts to check.")
				return nil
			}

			ctx := cmd.Context()
			anyNew := false
			for _, a := range toCheck {
				fmt.Printf("Checking alert %q (query: %s)...\n", a.Name, a.Query)

				resp, err := runAlertSearch(ctx, d, a)
				if err != nil {
					fmt.Fprintf(os.Stderr, "[warn] alert %q: %v\n", a.Name, err)
					continue
				}

				newHash := resultsHash([]search.Response{*resp})
				checkAlertResults(a, resp, newHash, &anyNew)
				updateAlertHash(allAlerts, a.Name, a.Project, newHash)
			}

			if err := saveAlerts(allAlerts); err != nil {
				return fmt.Errorf("save alerts: %w", err)
			}
			if anyNew {
				fmt.Println("\nSome alerts have new matches!")
			} else {
				fmt.Println("\nAll alerts checked — no new matches.")
			}
			return nil
		},
	}
	c.Flags().StringVar(&project, "project", "", "Only check alerts for this project")
	return c
}

func filterAlertsByProject(allAlerts []Alert, project string) []Alert {
	if project == "" {
		return allAlerts
	}
	var filtered []Alert
	for _, a := range allAlerts {
		if a.Project == project {
			filtered = append(filtered, a)
		}
	}
	return filtered
}

func runAlertSearch(ctx context.Context, d *deps, a Alert) (*search.Response, error) {
	if d.remote() {
		return searchAlertRemote(ctx, d, a)
	}
	return searchAlertLocal(ctx, d, a)
}

func searchAlertRemote(ctx context.Context, d *deps, a Alert) (*search.Response, error) {
	api := d.apiClient()
	p, err := searchtargets.ResolveRemoteProject(ctx, api, a.Project)
	if err != nil {
		return nil, fmt.Errorf("resolve project: %w", err)
	}
	sr, err := api.Search(ctx, p.Name, a.Query, client.SearchParams{TopK: 50})
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	resp := remoteToResponse(sr)
	return resp, nil
}

func searchAlertLocal(ctx context.Context, d *deps, a Alert) (*search.Response, error) {
	db, err := d.indexStore(ctx)
	if err != nil {
		return nil, err
	}
	targets, err := searchtargets.ResolveProjects(ctx, db, a.Project, "")
	if err != nil {
		return nil, fmt.Errorf("resolve project: %w", err)
	}
	req := search.Request{Query: a.Query, TopK: 50, KeywordOnly: d.keywordOnly}
	results, err := searchtargets.SearchLocal(ctx, db, d.emb, targets, req, gitmeta.Info{})
	if err != nil {
		return nil, fmt.Errorf("search error: %w", err)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no results returned")
	}
	resp := results[0].Resp
	return resp, nil
}

func checkAlertResults(a Alert, resp *search.Response, newHash string, anyNew *bool) {
	if a.LastHash == "" {
		fmt.Printf("  → Initial baseline recorded (%d results).\n", len(resp.Results))
	} else if newHash != a.LastHash {
		fmt.Printf("  ⚠ NEW MATCHES! Results have changed since last check.\n")
		*anyNew = true
		for _, r := range resp.Results {
			fmt.Printf("    %s:%d %s\n", r.FilePath, r.StartLine, truncateContent(r.Content, 80))
		}
	} else {
		fmt.Printf("  ✓ No new matches (%d results, unchanged).\n", len(resp.Results))
	}
}

func updateAlertHash(alerts []Alert, name, project, hash string) {
	for i := range alerts {
		if alerts[i].Name == name && alerts[i].Project == project {
			alerts[i].LastHash = hash
			return
		}
	}
}

// truncateContent shortens content for display, keeping the first n runes.
func truncateContent(content string, n int) string {
	runes := []rune(strings.TrimSpace(content))
	if len(runes) <= n {
		return string(runes)
	}
	return string(runes[:n]) + "..."
}
