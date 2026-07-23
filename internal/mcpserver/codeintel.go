package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lgldsilva/semidx/internal/analyzer"
	"github.com/lgldsilva/semidx/internal/codeintel"
	"github.com/lgldsilva/semidx/internal/deadcode"
)

// fileLineInput is the shared argument shape for the file:line code-intel tools
// (callers, explain, impact). impact additionally accepts a Depth field.
type fileLineInput struct {
	Project string `json:"project,omitempty" jsonschema:"the registered project name (optional when a default project is configured or cwd is inside an indexed project)"`
	File    string `json:"file" jsonschema:"project-relative path of the source file containing the symbol"`
	Line    int    `json:"line" jsonschema:"1-based line number of the symbol"`
	Depth   int    `json:"depth,omitempty" jsonschema:"max reverse-dependency depth (default 5, max 10)"`
}

type deadCodeInput struct {
	Project string `json:"project,omitempty" jsonschema:"the registered project name (optional when a default project is configured or cwd is inside an indexed project)"`
}

type diffInput struct {
	RefRange string `json:"ref_range" jsonschema:"git ref range as ref1..ref2 or ref1...ref2 (e.g. main..feat/x)"`
}

// registerCodeIntelTools registers the five code-intelligence MCP tools. They
// are always registered; remote backends return an in-band "standalone only"
// error from the Backend methods.
func registerCodeIntelTools(s *mcp.Server, b Backend, allowed map[string]bool, defaultProject string) {
	if allowed[toolSemanticCallers] {
		addProjectTool(s, &mcp.Tool{
			Name: toolSemanticCallers,
			Description: projectToolDescription(
				"Find files that import/depend on the package containing the symbol at file:line (who calls this?). Prefer this over grep when planning a refactor or checking blast radius of a change — it uses the indexed dependency graph, not text search.",
				defaultProject,
			),
		}, defaultProject, callersHandler(b, defaultProject))
	}
	if allowed[toolSemanticExplain] {
		addProjectTool(s, &mcp.Tool{
			Name: toolSemanticExplain,
			Description: projectToolDescription(
				"Explain a symbol at file:line: kind, location, dependencies, importers, and related tests. Use before editing an unfamiliar symbol to gather structural context faster than reading the whole package.",
				defaultProject,
			),
		}, defaultProject, explainHandler(b, defaultProject))
	}
	if allowed[toolSemanticImpact] {
		addProjectTool(s, &mcp.Tool{
			Name: toolSemanticImpact,
			Description: projectToolDescription(
				"Compute the blast radius of changing the symbol at file:line — all files transitively affected via reverse imports, bounded by depth. Use before risky refactors to list every dependent file.",
				defaultProject,
			),
		}, defaultProject, impactHandler(b, defaultProject))
	}
	if allowed[toolSemanticDeadCode] {
		addProjectTool(s, &mcp.Tool{
			Name: toolSemanticDeadCode,
			Description: projectToolDescription(
				"Find unused symbols in a project (packages with no importers). Use to clean dead code or audit public APIs that nothing references.",
				defaultProject,
			),
		}, defaultProject, deadCodeHandler(b, defaultProject))
	}
	if allowed[toolSemanticDiff] {
		mcp.AddTool(s, &mcp.Tool{
			Name:        toolSemanticDiff,
			Description: "Semantic symbol diff between two git refs (new/removed/changed funcs, types, consts). Prefer over plain git diff when you need symbol-level change summary. Arg ref_range is ref1..ref2 or ref1...ref2.",
		}, diffHandler(b))
	}
}

func callersHandler(b Backend, defaultProject string) mcp.ToolHandlerFor[fileLineInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in fileLineInput) (*mcp.CallToolResult, any, error) {
		if err := requireFileLine(in.File, in.Line); err != nil {
			return errorResult(err), nil, nil
		}
		out, err := b.Callers(ctx, resolveProject(in.Project, defaultProject), in.File, in.Line)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(formatCallers(out)), nil, nil
	}
}

func explainHandler(b Backend, defaultProject string) mcp.ToolHandlerFor[fileLineInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in fileLineInput) (*mcp.CallToolResult, any, error) {
		if err := requireFileLine(in.File, in.Line); err != nil {
			return errorResult(err), nil, nil
		}
		out, err := b.Explain(ctx, resolveProject(in.Project, defaultProject), in.File, in.Line)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(formatExplain(out)), nil, nil
	}
}

func impactHandler(b Backend, defaultProject string) mcp.ToolHandlerFor[fileLineInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in fileLineInput) (*mcp.CallToolResult, any, error) {
		if err := requireFileLine(in.File, in.Line); err != nil {
			return errorResult(err), nil, nil
		}
		out, err := b.Impact(ctx, resolveProject(in.Project, defaultProject), in.File, in.Line, in.Depth)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(formatImpact(out)), nil, nil
	}
}

func deadCodeHandler(b Backend, defaultProject string) mcp.ToolHandlerFor[deadCodeInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in deadCodeInput) (*mcp.CallToolResult, any, error) {
		out, err := b.DeadCode(ctx, resolveProject(in.Project, defaultProject))
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(formatDeadCode(out)), nil, nil
	}
}

func diffHandler(b Backend) mcp.ToolHandlerFor[diffInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in diffInput) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(in.RefRange) == "" {
			return errorResult(fmt.Errorf("ref_range is required (e.g. main..feat/x)")), nil, nil
		}
		out, err := b.Diff(ctx, in.RefRange)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(formatDiff(out)), nil, nil
	}
}

func requireFileLine(file string, line int) error {
	if strings.TrimSpace(file) == "" {
		return fmt.Errorf("file is required")
	}
	if line < 1 {
		return fmt.Errorf("line must be a positive 1-based integer")
	}
	return nil
}

func formatCallers(r *codeintel.CallersResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Callers of: %s\n", symbolDisplayName(r.Symbol))
	writeNamedList(&b, "Direct", r.Direct, "  (none — no indexed file imports this package)\n")
	if len(r.Transitive) > 0 {
		writeNamedList(&b, "Transitive", r.Transitive, "")
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatExplain(r *codeintel.ExplainResult) string {
	var b strings.Builder
	if r.Symbol != nil {
		fmt.Fprintf(&b, "%s — %s (%s:%d-%d)\n", r.Display, r.Symbol.Kind, r.File, r.Symbol.StartLine, r.Symbol.EndLine)
	} else {
		fmt.Fprintf(&b, "%s (%s)\n", r.Display, r.File)
	}
	writeNamedList(&b, "Dependencies", r.Imports, "  (none detected)\n")
	writeNamedList(&b, "Imported by", r.Importers, "  (none)\n")
	writeNamedList(&b, "Tests", r.Tests, "  (none found)\n")
	return strings.TrimRight(b.String(), "\n")
}

func formatImpact(r *codeintel.ImpactResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Impact of changing: %s\n", symbolDisplayName(r.Symbol))
	fmt.Fprintf(&b, "Affected files: %d\n", r.TotalCount)
	if r.TotalCount == 0 {
		b.WriteString("  (none — no reverse dependencies in the index)\n")
	} else {
		for _, n := range r.Affected {
			fmt.Fprintf(&b, "  [d=%d] %s\n", n.Depth, n.File)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatDeadCode(r *codeintel.DeadCodeResult) string {
	if r == nil || len(r.Findings) == 0 {
		return "No dead code found."
	}
	confirmed, publicAPI := partitionDeadCodeFindings(r.Findings)
	var b strings.Builder
	writeDeadCodeSection(&b, "Confirmed dead (safe to delete):", confirmed, false)
	writeDeadCodeSection(&b, "Likely dead (review needed):", publicAPI, b.Len() > 0)
	fmt.Fprintf(&b, "\nTotal dead: %d symbols (confirmed=%d, public-api=%d)",
		r.Stats.TotalFindings, r.Stats.Confirmed, r.Stats.PublicAPI)
	return strings.TrimRight(b.String(), "\n")
}

func formatDiff(r *codeintel.DiffResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Semantic Diff: %s → %s\n", r.Ref1, r.Ref2)
	if len(r.New)+len(r.Removed)+len(r.Changed) == 0 {
		b.WriteString("No semantic differences found.")
		return b.String()
	}
	writeDiffSection(&b, "New symbols", "+", r.New)
	writeDiffSection(&b, "Removed symbols", "-", r.Removed)
	writeChangedDiffSection(&b, r.Changed)
	return strings.TrimRight(b.String(), "\n")
}

// symbolDisplayName returns s.Name, or "(unknown)" when s is nil.
func symbolDisplayName(s *analyzer.Symbol) string {
	if s == nil {
		return "(unknown)"
	}
	return s.Name
}

// writeNamedList writes "Title (N):" plus indented items, or emptyMsg when N=0.
// emptyMsg must include its own trailing newline when non-empty; when items is
// non-empty emptyMsg is ignored.
func writeNamedList(b *strings.Builder, title string, items []string, emptyMsg string) {
	fmt.Fprintf(b, "%s (%d):\n", title, len(items))
	if len(items) == 0 {
		b.WriteString(emptyMsg)
		return
	}
	for _, item := range items {
		fmt.Fprintf(b, "  %s\n", item)
	}
}

func partitionDeadCodeFindings(findings []deadcode.Finding) (confirmed, publicAPI []string) {
	for _, f := range findings {
		line := fmt.Sprintf("%s:%d  %s (%s)", f.File, f.StartLine, f.Symbol, f.Kind)
		if f.Confidence == "confirmed" {
			confirmed = append(confirmed, line)
			continue
		}
		publicAPI = append(publicAPI, line)
	}
	return confirmed, publicAPI
}

func writeDeadCodeSection(b *strings.Builder, title string, lines []string, leadingBlank bool) {
	if len(lines) == 0 {
		return
	}
	if leadingBlank {
		b.WriteString("\n")
	}
	b.WriteString(title)
	b.WriteString("\n")
	for _, line := range lines {
		fmt.Fprintf(b, "  %s\n", line)
	}
}

func writeDiffSection(b *strings.Builder, title, mark string, diffs []codeintel.SymbolDiff) {
	if len(diffs) == 0 {
		return
	}
	fmt.Fprintf(b, "%s (%d):\n", title, len(diffs))
	for _, d := range diffs {
		fmt.Fprintf(b, "  %s %s (%s:%d) [%s]\n", mark, d.Name, d.FilePath, d.Line, d.Kind)
	}
}

// writeChangedDiffSection is extracted from formatDiff to keep its cognitive
// complexity under the SonarQube gate.
func writeChangedDiffSection(b *strings.Builder, diffs []codeintel.SymbolDiff) {
	if len(diffs) == 0 {
		return
	}
	fmt.Fprintf(b, "Changed signatures (%d):\n", len(diffs))
	for _, d := range diffs {
		fmt.Fprintf(b, "  ~ %s (%s:%d) [%s]\n", d.Name, d.FilePath, d.Line, d.Kind)
		if d.OldSignature != "" {
			fmt.Fprintf(b, "      old: %s\n", d.OldSignature)
		}
		if d.Signature != "" {
			fmt.Fprintf(b, "      new: %s\n", d.Signature)
		}
	}
}

// ErrCodeIntelStandaloneOnly is returned by remote/server backends for the
// code-intelligence tools, which currently run only against the local index.
func ErrCodeIntelStandaloneOnly(tool string) error {
	return fmt.Errorf("code-intelligence tool %q is available in standalone/local mode only; remote server support is not yet implemented", tool)
}
