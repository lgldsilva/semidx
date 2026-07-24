package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/mcpinstall"
	"github.com/lgldsilva/semidx/internal/skills"
)

func newDoctorCmd(d *deps) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check MCP install, skills, backend, and binary readiness",
		Long: `Inspect how semidx is wired on this machine: active backend, binary path,
which agent MCP configs contain a semidx entry, and whether bundled skills are
installed. Inspired by ai-memory's install-mcp / install-skills inventory.`,
		Example: `  semidx doctor`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd, d)
		},
	}
}

func runDoctor(cmd *cobra.Command, d *deps) error {
	out := cmd.OutOrStdout()
	bin, _ := os.Executable()
	fmt.Fprintf(out, "# semidx doctor\n\n")
	fmt.Fprintf(out, "## Binary\n\n- path: `%s`\n\n", bin)

	fmt.Fprintf(out, "## Backend\n\n")
	switch {
	case d.remote():
		fmt.Fprintf(out, "- active: **remote** (`%s`)\n", d.client.ServerURL)
	case d.localIndexPath != "":
		fmt.Fprintf(out, "- active: **local SQLite** (`%s`)\n", d.localIndexPath)
	default:
		fmt.Fprintf(out, "- active: **Postgres** (SEMIDX_DB_DSN configured or default)\n")
	}
	if d.hasServerConfig() && !d.remote() {
		fmt.Fprintf(out, "- note: server credentials exist on disk but this invocation is not using remote mode\n")
	}
	fmt.Fprintln(out)

	home, _ := os.UserHomeDir()
	cfgDir, _ := os.UserConfigDir()
	cwd, _ := os.Getwd()

	fmt.Fprintf(out, "## MCP clients\n\n")
	var missingClaude bool
	for _, c := range mcpinstall.Clients {
		opts := mcpinstall.Options{Client: c.ID, Home: home, ConfigDir: cfgDir, Project: cwd, ExePath: bin}
		path, err := mcpinstall.ConfigPath(opts)
		state := "absent"
		if err != nil {
			state = "error"
		} else if path != "" {
			if data, err := os.ReadFile(path); err == nil { // #nosec G304 -- path from known client locator
				if strings.Contains(string(data), "semidx") {
					state = "configured"
				} else {
					state = "config exists (no semidx entry)"
				}
			} else if !os.IsNotExist(err) {
				state = "unreadable"
			}
		}
		fmt.Fprintf(out, "- `%s`: %s", c.ID, state)
		if path != "" {
			fmt.Fprintf(out, " (`%s`)", path)
		}
		fmt.Fprintln(out)
		if c.ID == "claude-code" && state != "configured" {
			missingClaude = true
		}
	}
	fmt.Fprintln(out)

	fmt.Fprintf(out, "## Skills\n\n")
	names, err := skills.Names()
	if err != nil {
		fmt.Fprintf(out, "- error listing embedded skills: %v\n\n", err)
	} else {
		roots := []string{
			filepath.Join(home, ".claude", "skills"),
			filepath.Join(home, ".agents", "skills"),
			filepath.Join(home, ".cursor", "skills"),
			filepath.Join(cwd, ".claude", "skills"),
			filepath.Join(cwd, ".agents", "skills"),
		}
		for _, name := range names {
			found := []string{}
			for _, root := range roots {
				p := filepath.Join(root, name, "SKILL.md")
				if st, err := os.Stat(p); err == nil && !st.IsDir() {
					found = append(found, p)
				}
			}
			if len(found) == 0 {
				fmt.Fprintf(out, "- `%s`: not installed\n", name)
			} else {
				fmt.Fprintf(out, "- `%s`: installed\n", name)
				for _, p := range found {
					fmt.Fprintf(out, "  - `%s`\n", p)
				}
			}
		}
		fmt.Fprintln(out)
	}

	fmt.Fprintf(out, "## Findings\n\n")
	if missingClaude {
		fmt.Fprintf(out, "- **claude-code MCP missing** — run `semidx mcp install --client claude-code --apply`\n")
	}
	fmt.Fprintf(out, "- Search usage history: `semidx usage` (empty until searches are recorded)\n")
	fmt.Fprintf(out, "- Test/fixture projects named `semidx-*` may clutter `semantic_projects`; drop unused ones with `semidx drop`\n")
	return nil
}
