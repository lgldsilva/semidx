package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/store"
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

// runSbomGenerate executes the sbom generate logic. It is extracted to keep
// newSbomGenerateCmd's RunE under the cognitive complexity limit.
func runSbomGenerate(ctx context.Context, d *deps, projectPath, format, outputPath string, out io.Writer) error {
	db, err := d.indexStore(ctx)
	if err != nil {
		return err
	}

	// Resolve project — try identity (path), then name.
	tgt := resolveTarget(ctx, projectPath, false)
	proj, err := db.GetProjectByIdentity(ctx, tgt.identity)
	if err != nil {
		proj, err = db.GetProject(ctx, tgt.name)
		if err != nil {
			return fmt.Errorf("project not found — index it first with `semidx index --project %s`: %w", projectPath, err)
		}
	}

	components, err := collectComponents(ctx, db, proj)
	if err != nil {
		return fmt.Errorf("collect dependencies: %w", err)
	}

	var output []byte
	switch format {
	case "cyclonedx-json":
		output, err = generateCycloneDXJSON(proj, components)
	case "spdx-json":
		output, err = generateSPDXJSON(proj, components)
	default:
		return fmt.Errorf("unsupported format %q (supported: cyclonedx-json, spdx-json)", format)
	}
	if err != nil {
		return fmt.Errorf("generate %s: %w", format, err)
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

// ---------------------------------------------------------------------------
// Component collection
// ---------------------------------------------------------------------------

// sbomComponent is one entry in the SBOM.
type sbomComponent struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Type    string `json:"type"` // "file", "library", "module"
}

// collectComponents gathers all indexed file dependencies from the store.
func collectComponents(ctx context.Context, db store.IndexStore, proj *store.Project) ([]sbomComponent, error) {
	graph, err := db.FetchGraphNeighbors(ctx, proj.ID)
	if err != nil {
		return nil, err
	}

	sortedDeps := sortedUniqueDeps(graph)

	components := make([]sbomComponent, 0, len(sortedDeps))
	seen := make(map[string]bool)
	for _, dep := range sortedDeps {
		if seen[dep] {
			continue
		}
		seen[dep] = true
		components = append(components, sbomComponent{
			Name: dep,
			Type: "file",
		})
	}

	// Best-effort: try reading go.mod for version information.
	modInfo := readGoMod(proj.Path)
	components = applyGoModVersions(components, modInfo)

	return components, nil
}

// sortedUniqueDeps builds a sorted list of unique dependency targets from the graph.
func sortedUniqueDeps(graph map[string][]string) []string {
	depSet := make(map[string]bool)
	for _, targets := range graph {
		for _, t := range targets {
			depSet[t] = true
		}
	}

	sortedDeps := make([]string, 0, len(depSet))
	for d := range depSet {
		sortedDeps = append(sortedDeps, d)
	}
	sort.Strings(sortedDeps)
	return sortedDeps
}

// applyGoModVersions merges version info from go.mod into the components list
// (and appends the module entry if present). Nil modInfo is a no-op.
func applyGoModVersions(components []sbomComponent, modInfo *goModInfo) []sbomComponent {
	if modInfo == nil {
		return components
	}
	for i, c := range components {
		if v, ok := modInfo.versions[c.Name]; ok {
			components[i].Version = v
		}
	}
	if modInfo.module != "" {
		components = append(components, sbomComponent{
			Name:    modInfo.module,
			Version: modInfo.goVersion,
			Type:    "module",
		})
	}
	return components
}

// goModInfo is a lightweight view of a go.mod file.
type goModInfo struct {
	module    string
	goVersion string
	versions  map[string]string // dir path → version
}

// readGoMod parses a go.mod for module path and dependency versions.
func readGoMod(projectPath string) *goModInfo {
	// #nosec G304 -- projectPath is safe as it is bounded by the project configuration
	data, err := os.ReadFile(filepath.Clean(filepath.Join(projectPath, "go.mod")))
	if err != nil {
		return nil
	}
	info := &goModInfo{versions: make(map[string]string)}
	lines := strings.Split(string(data), "\n")
	inRequire := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if parseModuleLine(line, info) {
			continue
		}
		if parseGoVersionLine(line, info) {
			continue
		}
		if parseRequireBlockStart(line, &inRequire) {
			continue
		}
		if parseRequireBlockEnd(line, &inRequire) {
			continue
		}
		if inRequire {
			parseRequireLine(line, info)
		}
	}
	return info
}

// parseModuleLine sets info.module if the line starts with "module ".
// Returns true if it handled the line.
func parseModuleLine(line string, info *goModInfo) bool {
	if strings.HasPrefix(line, "module ") {
		info.module = strings.TrimSpace(strings.TrimPrefix(line, "module "))
		return true
	}
	return false
}

// parseGoVersionLine sets info.goVersion if the line starts with "go ".
// Returns true if it handled the line.
func parseGoVersionLine(line string, info *goModInfo) bool {
	if strings.HasPrefix(line, "go ") {
		info.goVersion = strings.TrimSpace(strings.TrimPrefix(line, "go "))
		return true
	}
	return false
}

// parseRequireBlockStart sets inRequire=true if the line starts a require block.
// Returns true if it handled the line.
func parseRequireBlockStart(line string, inRequire *bool) bool {
	if strings.HasPrefix(line, "require (") {
		*inRequire = true
		return true
	}
	return false
}

// parseRequireBlockEnd sets inRequire=false on a closing ")" for require block.
// Returns true if it handled the line.
func parseRequireBlockEnd(line string, inRequire *bool) bool {
	if line == ")" {
		*inRequire = false
		return true
	}
	return false
}

// parseRequireLine extracts a require entry inside a require block and populates
// info.versions using the module-relative dir key. It is a no-op for malformed lines.
func parseRequireLine(line string, info *goModInfo) {
	parts := strings.Fields(line)
	if len(parts) >= 2 {
		pkg := parts[0]
		ver := parts[1]
		dir := strings.TrimPrefix(pkg, info.module+"/") + "/"
		if dir != "" && dir != "/" {
			info.versions[dir] = ver
		}
	}
}

// ---------------------------------------------------------------------------
// CycloneDX 1.4 JSON
// ---------------------------------------------------------------------------

// cdxBOM is a minimal CycloneDX 1.4 document.
type cdxBOM struct {
	JSONSchema  string         `json:"$schema"`
	BOMFormat   string         `json:"bomFormat"`
	SpecVersion string         `json:"specVersion"`
	Version     int            `json:"version"`
	Metadata    cdxMetadata    `json:"metadata"`
	Components  []cdxComponent `json:"components"`
}

type cdxMetadata struct {
	Timestamp string       `json:"timestamp"`
	Tools     []cdxTool    `json:"tools"`
	Component cdxComponent `json:"component"`
}

type cdxTool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type cdxComponent struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	BOMRef  string `json:"bom-ref,omitempty"`
	Scope   string `json:"scope,omitempty"`
}

func generateCycloneDXJSON(proj *store.Project, components []sbomComponent) ([]byte, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	bom := cdxBOM{
		JSONSchema:  "http://cyclonedx.org/schema/bom-1.4.schema.json",
		BOMFormat:   "CycloneDX",
		SpecVersion: "1.4",
		Version:     1,
		Metadata: cdxMetadata{
			Timestamp: now,
			Tools: []cdxTool{{
				Name:    "semidx",
				Version: version,
			}},
			Component: cdxComponent{
				Type:   "application",
				Name:   proj.Name,
				BOMRef: proj.Identity,
			},
		},
		Components: make([]cdxComponent, 0, len(components)+1),
	}

	// Include the project itself as a component.
	bom.Components = append(bom.Components, cdxComponent{
		Type:   "application",
		Name:   proj.Name,
		BOMRef: "project-" + safeRef(proj.Identity),
	})

	for _, c := range components {
		ctype := "file"
		if c.Type == "module" {
			ctype = "library"
		}
		bom.Components = append(bom.Components, cdxComponent{
			Type:    ctype,
			Name:    c.Name,
			Version: c.Version,
			BOMRef:  safeRef(c.Name),
			Scope:   "required",
		})
	}

	return json.MarshalIndent(bom, "", "  ")
}

// ---------------------------------------------------------------------------
// SPDX 2.3 JSON
// ---------------------------------------------------------------------------

type spdxDoc struct {
	SPDXID            string             `json:"spdxId"`
	SPDXVersion       string             `json:"spdxVersion"`
	CreationInfo      spdxCreationInfo   `json:"creationInfo"`
	Name              string             `json:"name"`
	DataLicense       string             `json:"dataLicense"`
	DocumentNamespace string             `json:"documentNamespace"`
	Packages          []spdxPackage      `json:"packages"`
	Relationships     []spdxRelationship `json:"relationships,omitempty"`
}

type spdxCreationInfo struct {
	Created  string   `json:"created"`
	Creators []string `json:"creators"`
}

type spdxPackage struct {
	SPDXID           string `json:"SPDXID"`
	Name             string `json:"name"`
	VersionInfo      string `json:"versionInfo,omitempty"`
	PackageFileName  string `json:"packageFileName,omitempty"`
	FilesAnalyzed    bool   `json:"filesAnalyzed"`
	LicenseConcluded string `json:"licenseConcluded"`
	LicenseDeclared  string `json:"licenseDeclared"`
	CopyrightText    string `json:"copyrightText"`
}

type spdxRelationship struct {
	SPDXElementID      string `json:"spdxElementId"`
	RelatedSpdxElement string `json:"relatedSpdxElement"`
	RelationshipType   string `json:"relationshipType"`
}

func generateSPDXJSON(proj *store.Project, components []sbomComponent) ([]byte, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	namespace := fmt.Sprintf("https://semidx.local/sbom/%s/%d",
		safeRef(proj.Identity), time.Now().Unix())

	doc := spdxDoc{
		SPDXID:      "SPDXRef-DOCUMENT",
		SPDXVersion: "SPDX-2.3",
		CreationInfo: spdxCreationInfo{
			Created:  now,
			Creators: []string{fmt.Sprintf("Tool: semidx-%s", version)},
		},
		Name:              fmt.Sprintf("SBOM for %s", proj.Name),
		DataLicense:       "CC0-1.0",
		DocumentNamespace: namespace,
	}

	// Project package.
	doc.Packages = append(doc.Packages, spdxPackage{
		SPDXID:           "SPDXRef-Project",
		Name:             proj.Name,
		FilesAnalyzed:    false,
		LicenseConcluded: "NOASSERTION",
		LicenseDeclared:  "NOASSERTION",
		CopyrightText:    "NOASSERTION",
	})

	// Dependency packages.
	for _, c := range components {
		pkg := spdxPackage{
			SPDXID:           "SPDXRef-" + safeRef(c.Name),
			Name:             c.Name,
			VersionInfo:      c.Version,
			FilesAnalyzed:    false,
			LicenseConcluded: "NOASSERTION",
			LicenseDeclared:  "NOASSERTION",
			CopyrightText:    "NOASSERTION",
		}
		if c.Type == "file" {
			pkg.PackageFileName = c.Name
		}
		doc.Packages = append(doc.Packages, pkg)
	}

	// Relationships: project CONTAINS each dependency.
	for _, c := range components {
		doc.Relationships = append(doc.Relationships, spdxRelationship{
			SPDXElementID:      "SPDXRef-Project",
			RelatedSpdxElement: "SPDXRef-" + safeRef(c.Name),
			RelationshipType:   "CONTAINS",
		})
	}

	return json.MarshalIndent(doc, "", "  ")
}

// safeRef converts a name to a valid BOM reference.
func safeRef(name string) string {
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, ".", "-")
	name = strings.ReplaceAll(name, "@", "-")
	name = strings.ReplaceAll(name, ":", "-")
	name = strings.Trim(name, "-")
	if name == "" {
		return "component"
	}
	return name
}
