package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/extract"
	"github.com/lgldsilva/semidx/internal/mcpinstall"
	"github.com/lgldsilva/semidx/internal/pgcheck"
	"github.com/lgldsilva/semidx/internal/skills"
)

func newDoctorCmd(d *deps) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check MCP install, skills, backend, and binary readiness",
		Long: `Inspect how semidx is wired on this machine: active backend, binary path,
which agent MCP configs contain a semidx entry, whether bundled skills are
installed, and whether local Ollama reports GPU-resident models.`,
		Example: `  semidx doctor`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd, d)
		},
	}
}

func runDoctor(cmd *cobra.Command, d *deps) error {
	var b strings.Builder
	bin, _ := os.Executable()
	fmt.Fprintf(&b, "# semidx doctor\n\n")
	fmt.Fprintf(&b, "## Binary\n\n- path: `%s`\n\n", bin)
	reportBackend(&b, d)
	reportDatabase(cmd.Context(), &b, d)
	fmt.Fprintf(&b, "## Extractors\n\n")
	if extract.LegacyOfficeAvailable() {
		fmt.Fprintf(&b, "- legacy Office (.doc/.xls/.ppt): available\n\n")
	} else {
		fmt.Fprintf(&b, "- legacy Office (.doc/.xls/.ppt): disabled (libreoffice not in $PATH)\n\n")
	}
	fmt.Fprintf(&b, "## Ollama / GPU\n\n")
	printOllamaRuntime(&b, d)
	b.WriteByte('\n')

	home, _ := os.UserHomeDir()
	cfgDir, _ := os.UserConfigDir()
	cwd, _ := os.Getwd()
	missingClaude, configuredMCP := reportMCPClients(&b, home, cfgDir, cwd, bin)
	missingActivation := reportSkills(&b, home, cfgDir, cwd)
	reportFindings(&b, home, cfgDir, missingClaude, missingActivation, configuredMCP)
	_, err := fmt.Fprint(cmd.OutOrStdout(), b.String())
	return err
}

func reportBackend(b *strings.Builder, d *deps) {
	fmt.Fprintf(b, "## Backend\n\n")
	switch {
	case d.remote():
		fmt.Fprintf(b, "- active: **remote** (`%s`)\n", d.client.ServerURL)
		if host := d.client.ServerURL; strings.Contains(host, "raspberrypi.lan") || strings.Contains(host, ".lan/") {
			fmt.Fprintf(b, "- note: prefer a hostname covered by your TLS certificate (e.g. `*.internal…`) — mismatched SAN causes MCP TLS failures\n")
		}
	case d.localIndexPath != "":
		fmt.Fprintf(b, "- active: **local SQLite** (`%s`)\n", d.localIndexPath)
	default:
		fmt.Fprintf(b, "- active: **Postgres** (SEMIDX_DB_DSN configured or default)\n")
	}
	if d.hasServerConfig() && !d.remote() {
		fmt.Fprintf(b, "- note: server credentials exist on disk but this invocation is not using remote mode\n")
	}
	b.WriteByte('\n')
}

// reportDatabase probes the PostgreSQL backend when it is the active one. It is
// read-only (no migrations, no CREATE EXTENSION), so running doctor against a
// production database changes nothing.
func reportDatabase(ctx context.Context, b *strings.Builder, d *deps) {
	if d.remote() || d.localIndexPath != "" || d.cfg == nil || d.cfg.DatabaseURL == "" {
		return
	}
	fmt.Fprintf(b, "## Database\n\n")
	report, err := pgcheck.InspectDSN(ctx, d.cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintf(b, "- unreachable: %v\n", err)
		fmt.Fprintf(b, "- run `semidx docker` for a ready-to-use PostgreSQL/pgvector service\n\n")
		return
	}
	fmt.Fprintf(b, "- server: PostgreSQL %s\n", report.ServerVersion)
	for _, ext := range []pgcheck.Extension{report.Vector, report.Trgm} {
		if ext.Installed {
			fmt.Fprintf(b, "- extension `%s`: %s\n", ext.Name, ext.Version)
			continue
		}
		fmt.Fprintf(b, "- extension `%s`: missing\n", ext.Name)
	}
	if report.Vector.Installed {
		if report.HalfvecOpclass {
			fmt.Fprintf(b, "- halfvec HNSW: supported (models above 2000 dimensions can be indexed)\n")
		} else {
			fmt.Fprintf(b, "- halfvec HNSW: unsupported (models above 2000 dimensions cannot be indexed)\n")
		}
	}
	for _, f := range report.Findings() {
		fmt.Fprintf(b, "- ⚠️  %s\n", f)
	}
	b.WriteByte('\n')
}

func reportMCPClients(b *strings.Builder, home, cfgDir, cwd, bin string) (bool, map[string]bool) {
	fmt.Fprintf(b, "## MCP clients\n\n")
	missingClaude := false
	configuredMCP := map[string]bool{}
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
					configuredMCP[c.ID] = true
				} else {
					state = "config exists (no semidx entry)"
				}
			} else if !os.IsNotExist(err) {
				state = "unreadable"
			}
		}
		fmt.Fprintf(b, "- `%s`: %s", c.ID, state)
		if path != "" {
			fmt.Fprintf(b, " (`%s`)", path)
		}
		b.WriteByte('\n')
		if c.ID == "claude-code" && state != "configured" {
			missingClaude = true
		}
	}
	b.WriteByte('\n')
	return missingClaude, configuredMCP
}

func reportSkills(b *strings.Builder, home, cfgDir, cwd string) bool {
	fmt.Fprintf(b, "## Skills\n\n")
	names, err := skills.Names()
	missingActivation := false
	if err != nil {
		fmt.Fprintf(b, "- error listing embedded skills: %v\n\n", err)
		return missingActivation
	}

	roots := doctorSkillRoots(home, cfgDir, cwd)
	for _, name := range names {
		found := []string{}
		for _, root := range roots {
			p := filepath.Join(root, name, "SKILL.md")
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				found = append(found, p)
			}
		}
		if len(found) == 0 {
			fmt.Fprintf(b, "- `%s`: not installed\n", name)
			if name == "auto-index" {
				missingActivation = true
			}
			continue
		}
		fmt.Fprintf(b, "- `%s`: installed\n", name)
		for _, p := range found {
			fmt.Fprintf(b, "  - `%s`\n", p)
		}
	}
	b.WriteByte('\n')
	return missingActivation
}

func reportFindings(b *strings.Builder, home, cfgDir string, missingClaude, missingActivation bool, configuredMCP map[string]bool) {
	fmt.Fprintf(b, "## Findings\n\n")
	if missingClaude {
		fmt.Fprintf(b, "- **claude-code MCP missing** — run `semidx mcp install --client claude-code --apply`\n")
	}
	if missingActivation {
		fmt.Fprintf(b, "- **auto-index skill missing** — agents will not auto-route to semantic search; run `semidx skills install --all`\n")
	}
	for id := range configuredMCP {
		if t, ok := skillsTargetByID(id); ok && t.userLevel {
			root := t.path(home, cfgDir)
			if st, err := os.Stat(filepath.Join(root, "auto-index", "SKILL.md")); err != nil || st.IsDir() {
				fmt.Fprintf(b, "- **MCP `%s` configured but auto-index skill absent** — run `semidx skills install --target %s`\n", id, id)
			}
		}
	}
	fmt.Fprintf(b, "- Search usage history: `semidx usage` (empty until searches are recorded)\n")
	fmt.Fprintf(b, "- Test/fixture projects named `semidx-*` may clutter `semantic_projects`; drop unused ones with `semidx drop`\n")
	fmt.Fprintf(b, "- GPU for embeddings is owned by Ollama (probe above); Postgres/pgvector search stays on CPU\n")
}

// doctorSkillRoots returns every skills install target path plus legacy
// `.agents/skills` locations still scanned for completeness.
func doctorSkillRoots(home, configDir, cwd string) []string {
	seen := map[string]struct{}{}
	var roots []string
	add := func(p string) {
		if p == "" {
			return
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		roots = append(roots, p)
	}
	for _, t := range skillsTargets {
		add(t.path(home, configDir))
	}
	add(filepath.Join(home, ".agents", "skills"))
	add(filepath.Join(cwd, ".agents", "skills"))
	return roots
}
