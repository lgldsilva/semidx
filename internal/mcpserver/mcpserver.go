// Package mcpserver exposes semidx to AI agents over the Model Context Protocol.
// It is backend-agnostic: the three tools call a Backend, which is either a THIN
// client over the HTTP API (remote mode, pkg/client) or a local adapter over the
// standalone index (local mode, search.Service + the local store). Either way the
// tools only accept project NAMES (never filesystem paths), so an agent can never
// trigger indexing of an arbitrary path — only already-registered/indexed projects
// are reachable.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/repotools"
	"github.com/lgldsilva/semidx/internal/search"
)

const version = "0.1.0"

const mimeApplicationJSON = "application/json"

// Hit is one ranked search result, independent of the backend that produced it.
type Hit struct {
	Path      string
	StartLine int
	EndLine   int
	Score     float64
	Content   string
	Language  string
}

// SearchOutput is a backend-neutral search result set.
type SearchOutput struct {
	Project  string
	Fallback bool
	// Degraded is true when the embedding circuit was open and the backend
	// served keyword results instead of failing; RetryAfterMS hints when the
	// embedding provider may recover.
	Degraded     bool
	RetryAfterMS int64
	TookMS       int64
	Results      []Hit
}

// ProjectInfo is a backend-neutral project summary.
type ProjectInfo struct {
	Name       string
	SourceType string
	GitURL     string
	Status     string
	Model      string
}

// StatusInfo is a backend-neutral project status summary.
type StatusInfo struct {
	Name       string
	SourceType string
	Identity   string
	Status     string
	Model      string
	TotalFiles int
}

// Backend is the data source the MCP tools call. Implemented by the remote HTTP
// client (NewClientBackend) and the local index (NewLocalBackend).
type Backend interface {
	Search(ctx context.Context, project, query, model string, topK int, graph bool, graphDepth int) (*SearchOutput, error)
	Projects(ctx context.Context) ([]ProjectInfo, error)
	// Reindex returns a human-readable status message on success.
	Reindex(ctx context.Context, project, jobType string) (string, error)
	// Status returns the indexing status of a registered project.
	Status(ctx context.Context, project string) (*StatusInfo, error)
	// Capabilities reports what the current runtime backend can do (local git,
	// chat LLM, etc.). Used for tool gating and agent introspection.
	Capabilities() agent.Capabilities
}

// GitBackend extends Backend with read-only git workspace tools. Only
// implemented by the local backend (remote clients cannot see worktrees).
type GitBackend interface {
	Backend
	Worktrees(ctx context.Context, project string) ([]repotools.Worktree, error)
	Branches(ctx context.Context, project string, remote bool) ([]repotools.Branch, error)
	GitStatus(ctx context.Context, project string) (*repotools.RepoStatus, error)
}

// MultiSearchBackend extends Backend with cross-project search. Implemented
// by the local backend; the remote backends may support it later.
type MultiSearchBackend interface {
	Backend
	SearchMulti(ctx context.Context, req search.MultiScopeRequest) (*search.MultiResponse, error)
}

// unwrapper is implemented by backend wrappers (e.g. the agentic/RAG ask
// backends) that embed another Backend. It lets tool gating see the wrapped
// backend's extra capabilities (GitBackend, MultiSearchBackend) which interface
// embedding would otherwise hide.
type unwrapper interface{ Unwrap() Backend }

// asGitBackend finds a GitBackend in b or anywhere down its wrapped chain.
func asGitBackend(b Backend) (GitBackend, bool) {
	for b != nil {
		if gb, ok := b.(GitBackend); ok {
			return gb, true
		}
		u, ok := b.(unwrapper)
		if !ok {
			return nil, false
		}
		b = u.Unwrap()
	}
	return nil, false
}

// asMultiSearchBackend finds a MultiSearchBackend in b or its wrapped chain.
func asMultiSearchBackend(b Backend) (MultiSearchBackend, bool) {
	for b != nil {
		if mb, ok := b.(MultiSearchBackend); ok {
			return mb, true
		}
		u, ok := b.(unwrapper)
		if !ok {
			return nil, false
		}
		b = u.Unwrap()
	}
	return nil, false
}

// New builds an MCP server whose tools call the given backend.
func New(b Backend) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "semidx", Version: version}, nil)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "semantic_search",
		Description: "Search a registered project's indexed code semantically with a natural-language query. Returns ranked file:line matches with a content preview. Prefer this over plain grep when the query is about intent or behavior rather than an exact string.",
	}, searchHandler(b))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "semantic_projects",
		Description: "List the projects registered in this semidx index, with their indexing status.",
	}, projectsHandler(b))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "semantic_reindex",
		Description: "Queue a re-index job for a project already registered on the server. Only registered projects can be re-indexed; arbitrary paths are not accepted. In standalone (local) mode, reindex via the `semidx index` CLI instead.",
	}, reindexHandler(b))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "semantic_status",
		Description: "Get the indexing status of a registered project. Reports file count, status, and model.",
	}, statusHandler(b))

	// Register git tools when the backend (or a backend it wraps) implements
	// GitBackend — an ask wrapper must not hide the local backend's git tools.
	if gitB, ok := asGitBackend(b); ok {
		mcp.AddTool(s, &mcp.Tool{
			Name:        "repo_worktrees",
			Description: "List all worktrees of a repository (requires local git access). On server mode, returns unsupported.",
		}, gitWorktreesHandler(gitB))

		mcp.AddTool(s, &mcp.Tool{
			Name:        "repo_branches",
			Description: "List branches of a repository. Includes remote branches when --remote is true.",
		}, gitBranchesHandler(gitB))

		mcp.AddTool(s, &mcp.Tool{
			Name:        "repo_status",
			Description: "Show the repository working tree status (dirty, current branch, HEAD SHA).",
		}, gitStatusHandler(gitB))
	}

	// Register multi-scope search when the backend (or one it wraps) implements
	// MultiSearchBackend.
	if msB, ok := asMultiSearchBackend(b); ok {
		mcp.AddTool(s, &mcp.Tool{
			Name:        "semantic_search_multi",
			Description: "Search across multiple projects in one query, with fused results and project labels.",
		}, multiSearchHandler(msB))
	}

	// Register the semantic_ask tool only when the backend also implements AskBackend.
	if askBackend, ok := b.(AskBackend); ok {
		mcp.AddTool(s, &mcp.Tool{
			Name:        "semantic_ask",
			Description: "Ask a question about a registered project — RAG-augmented chat over indexed code. Returns an answer with cited source chunks.",
		}, askHandler(askBackend))
	}

	// ---- Resources ----
	s.AddResource(&mcp.Resource{
		URI:         "semidx://projects",
		Name:        "Projects",
		MIMEType:    mimeApplicationJSON,
		Description: "List of indexed projects with their indexing status, model, and source type.",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		projects, err := b.Projects(ctx)
		if err != nil {
			return nil, fmt.Errorf("list projects: %w", err)
		}
		type projectRow struct {
			Name       string `json:"name"`
			SourceType string `json:"source_type"`
			GitURL     string `json:"git_url,omitempty"`
			Status     string `json:"status"`
			Model      string `json:"model"`
		}
		rows := make([]projectRow, len(projects))
		for i, p := range projects {
			rows[i] = projectRow(p)
		}
		data, _ := json.MarshalIndent(rows, "", "  ")
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      "semidx://projects",
				MIMEType: mimeApplicationJSON,
				Text:     string(data),
			}},
		}, nil
	})

	s.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "semidx://project/{name}/stats",
		Name:        "Project Stats",
		MIMEType:    mimeApplicationJSON,
		Description: "Indexing statistics for a specific project: file count, chunk count, languages, and model info.",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		uri := req.Params.URI
		// Extract project name from URI template: semidx://project/{name}/stats
		name := strings.TrimPrefix(uri, "semidx://project/")
		name = strings.TrimSuffix(name, "/stats")
		if name == "" || strings.Contains(name, "/") {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		info, err := b.Status(ctx, name)
		if err != nil {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		type statsRow struct {
			Name       string `json:"name"`
			SourceType string `json:"source_type"`
			Identity   string `json:"identity,omitempty"`
			Status     string `json:"status"`
			Model      string `json:"model"`
			TotalFiles int    `json:"total_files"`
		}
		stats := statsRow{
			Name: info.Name, SourceType: info.SourceType, Identity: info.Identity,
			Status: info.Status, Model: info.Model, TotalFiles: info.TotalFiles,
		}
		data, _ := json.MarshalIndent(stats, "", "  ")
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      uri,
				MIMEType: mimeApplicationJSON,
				Text:     string(data),
			}},
		}, nil
	})

	return s
}

// Run serves the MCP protocol over stdio until the client disconnects or ctx is
// cancelled.
func Run(ctx context.Context, b Backend) error {
	return New(b).Run(ctx, &mcp.StdioTransport{})
}

type searchInput struct {
	Project    string `json:"project" jsonschema:"the registered project name to search"`
	Query      string `json:"query" jsonschema:"the natural-language search query"`
	Model      string `json:"model,omitempty" jsonschema:"optional embedding model override (defaults to the project's model)"`
	TopK       int    `json:"top_k,omitempty" jsonschema:"number of results to return (default 5)"`
	Graph      bool   `json:"graph,omitempty" jsonschema:"expand results via dependency graph (Graph-RAG)"`
	GraphDepth int    `json:"graph_depth,omitempty" jsonschema:"max BFS depth for graph expansion (default 2)"`
	Format     string `json:"format,omitempty" jsonschema:"output format: structured (default, JSON), text (legacy plain text), or minimal (compact JSON with abbreviated keys)"`
}

func searchHandler(b Backend) mcp.ToolHandlerFor[searchInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, any, error) {
		topK := in.TopK
		if topK == 0 {
			topK = 5
		}
		graphDepth := search.ClampGraphDepth(in.GraphDepth)
		start := time.Now()
		out, err := b.Search(ctx, in.Project, in.Query, in.Model, topK, in.Graph, graphDepth)
		if err != nil {
			return errorResult(err), nil, nil
		}
		out.TookMS = time.Since(start).Milliseconds()

		switch in.Format {
		case "text":
			return textResult(formatSearchText(out)), nil, nil
		case "minimal":
			return textResult(formatSearchMinimal(out)), nil, nil
		default: // "structured" or unspecified
			return textResult(formatSearchStructured(out)), nil, nil
		}
	}
}

type projectsInput struct{}

func projectsHandler(b Backend) mcp.ToolHandlerFor[projectsInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ projectsInput) (*mcp.CallToolResult, any, error) {
		projects, err := b.Projects(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(formatProjects(projects)), nil, nil
	}
}

type reindexInput struct {
	Project string `json:"project" jsonschema:"the registered project to re-index"`
	Type    string `json:"type,omitempty" jsonschema:"job type: full or git_history (default full)"`
}

func reindexHandler(b Backend) mcp.ToolHandlerFor[reindexInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in reindexInput) (*mcp.CallToolResult, any, error) {
		jobType := in.Type
		if jobType == "" {
			jobType = "full"
		}
		msg, err := b.Reindex(ctx, in.Project, jobType)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(msg), nil, nil
	}
}

type statusInput struct {
	Project string `json:"project" jsonschema:"the registered project name to check status for"`
}

func statusHandler(b Backend) mcp.ToolHandlerFor[statusInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in statusInput) (*mcp.CallToolResult, any, error) {
		info, err := b.Status(ctx, in.Project)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(formatStatus(info)), nil, nil
	}
}

// ---- Git tool handlers ----

type gitWorktreesInput struct {
	Project string `json:"project" jsonschema:"the registered project name"`
}

func gitWorktreesHandler(b GitBackend) mcp.ToolHandlerFor[gitWorktreesInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in gitWorktreesInput) (*mcp.CallToolResult, any, error) {
		wts, err := b.Worktrees(ctx, in.Project)
		if err != nil {
			return errorResult(err), nil, nil
		}
		data, _ := json.Marshal(wts)
		return textResult(string(data)), nil, nil
	}
}

type gitBranchesInput struct {
	Project string `json:"project" jsonschema:"the registered project name"`
	Remote  bool   `json:"remote,omitempty" jsonschema:"include remote branches (default false)"`
}

func gitBranchesHandler(b GitBackend) mcp.ToolHandlerFor[gitBranchesInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in gitBranchesInput) (*mcp.CallToolResult, any, error) {
		branches, err := b.Branches(ctx, in.Project, in.Remote)
		if err != nil {
			return errorResult(err), nil, nil
		}
		data, _ := json.Marshal(branches)
		return textResult(string(data)), nil, nil
	}
}

type gitStatusInput struct {
	Project string `json:"project" jsonschema:"the registered project name"`
}

func gitStatusHandler(b GitBackend) mcp.ToolHandlerFor[gitStatusInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in gitStatusInput) (*mcp.CallToolResult, any, error) {
		status, err := b.GitStatus(ctx, in.Project)
		if err != nil {
			return errorResult(err), nil, nil
		}
		data, _ := json.Marshal(status)
		return textResult(string(data)), nil, nil
	}
}

// ---- Multi-search handler ----

type multiSearchInput struct {
	Identities    []string `json:"identities" jsonschema:"project identities to search (git identity or path:identity)"`
	Query         string   `json:"query" jsonschema:"the natural-language search query"`
	TopK          int      `json:"top_k,omitempty" jsonschema:"number of results to return (default 5)"`
	Graph         bool     `json:"graph,omitempty" jsonschema:"expand results via dependency graph (Graph-RAG)"`
	GraphDepth    int      `json:"graph_depth,omitempty" jsonschema:"max BFS depth for graph expansion (default 2)"`
	MaxPerFile    int      `json:"max_per_file,omitempty" jsonschema:"cap chunks per file for diversity (default 3)"`
	MaxPerProject int      `json:"max_per_project,omitempty" jsonschema:"cap results per project for diversity (default 10)"`
}

func multiSearchHandler(b MultiSearchBackend) mcp.ToolHandlerFor[multiSearchInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in multiSearchInput) (*mcp.CallToolResult, any, error) {
		req := search.MultiScopeRequest{
			Identities:    in.Identities,
			Query:         in.Query,
			TopK:          in.TopK,
			Graph:         in.Graph,
			GraphMaxDepth: search.ClampGraphDepth(in.GraphDepth),
			MaxPerFile:    in.MaxPerFile,
			MaxPerProject: in.MaxPerProject,
		}
		if req.TopK <= 0 {
			req.TopK = 5
		}
		// Apply the published diversity defaults when the client omits them —
		// otherwise SearchMulti treats 0/0 as "no caps", letting one file or
		// project dominate, contrary to the tool's documented behavior.
		if req.MaxPerFile <= 0 {
			req.MaxPerFile = 3
		}
		if req.MaxPerProject <= 0 {
			req.MaxPerProject = 10
		}
		out, err := b.SearchMulti(ctx, req)
		if err != nil {
			return errorResult(err), nil, nil
		}
		data, _ := json.Marshal(out)
		return textResult(string(data)), nil, nil
	}
}

func formatStatus(info *StatusInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Project: %s\n", info.Name)
	if info.Identity != "" {
		fmt.Fprintf(&b, "Identity: %s\n", info.Identity)
	}
	fmt.Fprintf(&b, "Source: %s\n", info.SourceType)
	fmt.Fprintf(&b, "Status: %s\n", info.Status)
	if info.Model != "" {
		fmt.Fprintf(&b, "Model: %s\n", info.Model)
	}
	fmt.Fprintf(&b, "Total indexed: %d files\n", info.TotalFiles)
	fmt.Fprintln(&b)
	fmt.Fprint(&b, "Tip: projects may have unindexed changes. Use semantic_reindex (server mode) or `semidx index` (standalone) to refresh.")
	return strings.TrimRight(b.String(), "\n")
}

func formatSearchText(out *SearchOutput) string {
	var b strings.Builder
	if out.Degraded {
		fmt.Fprintf(&b, "*[degraded: embedding temporarily unavailable — keyword results; retry in ~%ds]*\n\n",
			search.RetrySeconds(out.RetryAfterMS))
	} else if out.Fallback {
		b.WriteString("[warning] embedding was unavailable — results come from keyword search, not semantic ranking.\n\n")
	}
	if len(out.Results) == 0 {
		fmt.Fprintf(&b, "No results in project %q for that query.", out.Project)
		return b.String()
	}
	for i, r := range out.Results {
		var loc string
		if r.EndLine > r.StartLine {
			loc = fmt.Sprintf("%s:%d-%d", r.Path, r.StartLine, r.EndLine)
		} else {
			loc = fmt.Sprintf("%s:%d", r.Path, r.StartLine)
		}
		fmt.Fprintf(&b, "%d. %s  (score %.3f)\n%s\n\n", i+1, loc, r.Score, preview(r.Content, 300))
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatProjects(projects []ProjectInfo) string {
	if len(projects) == 0 {
		return "No projects are registered in this index."
	}
	var b strings.Builder
	for _, p := range projects {
		src := p.SourceType
		if p.GitURL != "" {
			src = fmt.Sprintf("%s (%s)", p.SourceType, p.GitURL)
		}
		fmt.Fprintf(&b, "- %s  [%s]  status=%s  model=%s\n", p.Name, src, p.Status, p.Model)
	}
	return strings.TrimRight(b.String(), "\n")
}

func preview(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// errorResult surfaces a business error to the agent as tool content with
// IsError set — an in-band error the model can read and react to, not a
// protocol-level failure.
func errorResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}
}

// ---- structured result (default format) ----

// structuredHit is the JSON shape for one search result in structured mode.
type structuredHit struct {
	File      string  `json:"file"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Score     float64 `json:"score"`
	Language  string  `json:"language,omitempty"`
	Content   string  `json:"content"`
	Project   string  `json:"project"`
}

// structuredOutput is the JSON envelope for structured search results.
type structuredOutput struct {
	Results      []structuredHit `json:"results"`
	Fallback     bool            `json:"fallback"`
	Degraded     bool            `json:"degraded"`
	RetryAfterMS int64           `json:"retry_after_ms"`
	Total        int             `json:"total_results"`
	QueryTimeMS  int64           `json:"query_time_ms"`
}

func formatSearchStructured(out *SearchOutput) string {
	envelope := structuredOutput{
		Fallback: out.Fallback, Degraded: out.Degraded, RetryAfterMS: out.RetryAfterMS,
		QueryTimeMS: out.TookMS,
	}
	if len(out.Results) == 0 {
		data, _ := json.Marshal(envelope)
		return string(data)
	}
	hits := make([]structuredHit, len(out.Results))
	for i, r := range out.Results {
		lang := r.Language
		if lang == "" {
			lang = detectLanguage(r.Path)
		}
		hits[i] = structuredHit{
			File:      r.Path,
			StartLine: r.StartLine,
			EndLine:   r.EndLine,
			Score:     r.Score,
			Language:  lang,
			Content:   r.Content,
			Project:   out.Project,
		}
	}
	envelope.Results = hits
	envelope.Total = len(hits)
	data, _ := json.Marshal(envelope)
	return string(data)
}

// ---- minimal format (abbreviated keys, ~60% token savings) ----

// minimalHit is the compact JSON shape for one search result.
type minimalHit struct {
	F string  `json:"f"` // file path
	L string  `json:"l"` // line range ("start-end" or "start")
	S float64 `json:"s"` // score
	C string  `json:"c"` // content preview
}

// minimalOutput is the compact JSON envelope.
type minimalOutput struct {
	R  []minimalHit `json:"r"`  // results
	Fb bool         `json:"fb"` // fallback
	Dg bool         `json:"dg"` // degraded (embed circuit open, keyword results)
	Ra int64        `json:"ra"` // retry-after hint in ms (when degraded)
	T  int          `json:"t"`  // total
	Ms int64        `json:"ms"` // query time ms
}

func formatSearchMinimal(out *SearchOutput) string {
	if len(out.Results) == 0 {
		data, _ := json.Marshal(minimalOutput{Fb: out.Fallback, Dg: out.Degraded, Ra: out.RetryAfterMS, T: 0, Ms: out.TookMS})
		return string(data)
	}
	hits := make([]minimalHit, len(out.Results))
	for i, r := range out.Results {
		lineRange := fmt.Sprintf("%d", r.StartLine)
		if r.EndLine > r.StartLine {
			lineRange = fmt.Sprintf("%d-%d", r.StartLine, r.EndLine)
		}
		hits[i] = minimalHit{
			F: r.Path,
			L: lineRange,
			S: r.Score,
			C: preview(r.Content, 120),
		}
	}
	outJSON := minimalOutput{R: hits, Fb: out.Fallback, Dg: out.Degraded, Ra: out.RetryAfterMS, T: len(hits), Ms: out.TookMS}
	data, _ := json.Marshal(outJSON)
	return string(data)
}

// ---- language detection helper ----

// detectLanguage returns a language name for a file path, based on its extension.
// Used for populating the "language" field in structured results.
func detectLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".mts", ".cts":
		return "typescript"
	case ".tsx":
		return "tsx"
	case ".jsx":
		return "jsx"
	case ".py":
		return "python"
	case ".java":
		return "java"
	case ".rs":
		return "rust"
	case ".rb":
		return "ruby"
	case ".c":
		return "c"
	case ".cpp", ".cc", ".cxx":
		return "cpp"
	case ".h", ".hpp":
		return "c-header"
	case ".cs":
		return "csharp"
	case ".swift":
		return "swift"
	case ".kt", ".kts":
		return "kotlin"
	case ".scala":
		return "scala"
	case ".php":
		return "php"
	case ".r", ".rdata":
		return "r"
	case ".sql":
		return "sql"
	case ".sh", ".bash", ".zsh":
		return "shell"
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	case ".xml", ".html", ".htm":
		return "html"
	case ".css":
		return "css"
	case ".md", ".markdown":
		return "markdown"
	case ".proto":
		return "protobuf"
	case ".lua":
		return "lua"
	case ".ex", ".exs":
		return "elixir"
	default:
		return ""
	}
}
