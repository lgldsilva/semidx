package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/sbom"
)

// newSbomCmd creates the "sbom" command group under "advanced".
func newSbomCmd(d *deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sbom",
		Short: "Generate a Software Bill of Materials from indexed dependencies",
		Long: `Generate a Software Bill of Materials (SBOM) from the dependencies extracted
during indexing.  Dependencies are read from the file_dependencies table,
augmented with version information from go.mod when available.

Supported output formats:
  - cyclonedx-json  (CycloneDX 1.4 JSON, the default)
  - spdx-json       (SPDX 2.3 JSON)

The SBOM covers intra-project dependencies extracted by the indexer's
import analysis.  For Go projects, version information from go.mod is included
when the project uses Go modules.`,
		Example: `  semidx sbom generate --project myapp
  semidx sbom generate --project . --format spdx-json
  semidx sbom generate --project . --output sbom.json`,
	}
	cmd.AddCommand(newSbomGenerateCmd(d))
	return cmd
}

func newSbomGenerateCmd(d *deps) *cobra.Command {
	var (
		projectPath string
		format      string
		outputPath  string
	)
	c := &cobra.Command{
		Use:   "generate",
		Short: "Generate an SBOM for an indexed project",
		Example: `  semidx sbom generate --project .                              # CycloneDX JSON
  semidx sbom generate --project myapp --format spdx-json     # SPDX 2.3 JSON
  semidx sbom generate --project . --output sbom.cdx.json     # write to file`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSbomGenerate(cmd.Context(), d, projectPath, format, outputPath, cmd.OutOrStdout())
		},
	}
	c.Flags().StringVar(&projectPath, "project", ".", "Path or name of the indexed project")
	c.Flags().StringVar(&format, "format", "cyclonedx-json", "Output format (cyclonedx-json, spdx-json)")
	c.Flags().StringVar(&outputPath, "output", "", "Write output to file instead of stdout")
	return c
}

func runSbomGenerate(ctx context.Context, d *deps, projectPath, format, outputPath string, out io.Writer) error {
	db, err := d.indexStore(ctx)
	if err != nil {
		return err
	}

	tgt := resolveTarget(ctx, projectPath, false)
	proj, err := db.GetProjectByIdentity(ctx, tgt.identity)
	if err != nil {
		proj, err = db.GetProject(ctx, tgt.name)
		if err != nil {
			return fmt.Errorf("project not found — index it first with `semidx index --project %s`: %w", projectPath, err)
		}
	}

	output, err := sbom.Generate(ctx, db, proj, format, version)
	if err != nil {
		return err
	}

	if outputPath != "" {
		if err := os.WriteFile(filepath.Clean(outputPath), output, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", outputPath, err)
		}
		_, _ = fmt.Fprintf(out, "SBOM written to %s (%s)\n", outputPath, format)
	} else {
		_, _ = out.Write(output)
		_, _ = fmt.Fprintln(out)
	}
	return nil
}
