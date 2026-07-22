package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lgldsilva/semidx/internal/codeintel"
)

type callersInput struct {
	Project string `json:"project,omitempty" jsonschema:"the registered project name (optional when a default project is configured or cwd is inside an indexed project)"`
	File    string `json:"file" jsonschema:"project-relative path of the source file containing the symbol"`
	Line    int    `json:"line" jsonschema:"1-based line number of the symbol"`
}

type explainInput struct {
	Project string `json:"project,omitempty" jsonschema:"the registered project name (optional when a default project is configured or cwd is inside an indexed project)"`
	File    string `json:"file" jsonschema:"project-relative path of the source file containing the symbol"`
	Line    int    `json:"line" jsonschema:"1-based line number of the symbol"`
}

type impactInput struct {
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

func callersHandler(b Backend, defaultProject string) mcp.ToolHandlerFor[callersInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in callersInput) (*mcp.CallToolResult, any, error) {
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

func explainHandler(b Backend, defaultProject string) mcp.ToolHandlerFor[explainInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in explainInput) (*mcp.CallToolResult, any, error) {
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

func impactHandler(b Backend, defaultProject string) mcp.ToolHandlerFor[impactInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in impactInput) (*mcp.CallToolResult, any, error) {
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
	name := "(unknown)"
	if r.Symbol != nil {
		name = r.Symbol.Name
	}
	fmt.Fprintf(&b, "Callers of: %s\n", name)
	fmt.Fprintf(&b, "Direct (%d):\n", len(r.Direct))
	if len(r.Direct) == 0 {
		b.WriteString("  (none — no indexed file imports this package)\n")
	} else {
		for _, c := range r.Direct {
			fmt.Fprintf(&b, "  %s\n", c)
		}
	}
	if len(r.Transitive) > 0 {
		fmt.Fprintf(&b, "Transitive (%d):\n", len(r.Transitive))
		for _, t := range r.Transitive {
			fmt.Fprintf(&b, "  %s\n", t)
		}
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
	fmt.Fprintf(&b, "Dependencies (%d):\n", len(r.Imports))
	if len(r.Imports) == 0 {
		b.WriteString("  (none detected)\n")
	} else {
		for _, dep := range r.Imports {
			fmt.Fprintf(&b, "  %s\n", dep)
		}
	}
	fmt.Fprintf(&b, "Imported by (%d):\n", len(r.Importers))
	if len(r.Importers) == 0 {
		b.WriteString("  (none)\n")
	} else {
		for _, imp := range r.Importers {
			fmt.Fprintf(&b, "  %s\n", imp)
		}
	}
	fmt.Fprintf(&b, "Tests (%d):\n", len(r.Tests))
	if len(r.Tests) == 0 {
		b.WriteString("  (none found)\n")
	} else {
		for _, tf := range r.Tests {
			fmt.Fprintf(&b, "  %s\n", tf)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatImpact(r *codeintel.ImpactResult) string {
	var b strings.Builder
	name := "(unknown)"
	if r.Symbol != nil {
		name = r.Symbol.Name
	}
	fmt.Fprintf(&b, "Impact of changing: %s\n", name)
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
	var b strings.Builder
	var confirmed, publicAPI []string
	for _, f := range r.Findings {
		line := fmt.Sprintf("%s:%d  %s (%s)", f.File, f.StartLine, f.Symbol, f.Kind)
		switch f.Confidence {
		case "confirmed":
			confirmed = append(confirmed, line)
		default:
			publicAPI = append(publicAPI, line)
		}
	}
	if len(confirmed) > 0 {
		b.WriteString("Confirmed dead (safe to delete):\n")
		for _, line := range confirmed {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	if len(publicAPI) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("Likely dead (review needed):\n")
		for _, line := range publicAPI {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	fmt.Fprintf(&b, "\nTotal dead: %d symbols (confirmed=%d, public-api=%d)",
		r.Stats.TotalFindings, r.Stats.Confirmed, r.Stats.PublicAPI)
	return strings.TrimRight(b.String(), "\n")
}

func formatDiff(r *codeintel.DiffResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Semantic Diff: %s → %s\n", r.Ref1, r.Ref2)
	total := len(r.New) + len(r.Removed) + len(r.Changed)
	if total == 0 {
		b.WriteString("No semantic differences found.")
		return b.String()
	}
	if len(r.New) > 0 {
		fmt.Fprintf(&b, "New symbols (%d):\n", len(r.New))
		for _, d := range r.New {
			fmt.Fprintf(&b, "  + %s (%s:%d) [%s]\n", d.Name, d.FilePath, d.Line, d.Kind)
		}
	}
	if len(r.Removed) > 0 {
		fmt.Fprintf(&b, "Removed symbols (%d):\n", len(r.Removed))
		for _, d := range r.Removed {
			fmt.Fprintf(&b, "  - %s (%s:%d) [%s]\n", d.Name, d.FilePath, d.Line, d.Kind)
		}
	}
	if len(r.Changed) > 0 {
		fmt.Fprintf(&b, "Changed signatures (%d):\n", len(r.Changed))
		for _, d := range r.Changed {
			fmt.Fprintf(&b, "  ~ %s (%s:%d) [%s]\n", d.Name, d.FilePath, d.Line, d.Kind)
			if d.OldSignature != "" {
				fmt.Fprintf(&b, "      old: %s\n", d.OldSignature)
			}
			if d.Signature != "" {
				fmt.Fprintf(&b, "      new: %s\n", d.Signature)
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// errCodeIntelRemote is the shared remote/server stub message.
func errCodeIntelRemote(toolName string) error {
	return fmt.Errorf("code-intelligence tool %q is available in standalone/local mode only; remote server support is not yet implemented", toolName)
}
