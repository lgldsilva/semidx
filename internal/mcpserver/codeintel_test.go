package mcpserver

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/analyzer"
	"github.com/lgldsilva/semidx/internal/codeintel"
	"github.com/lgldsilva/semidx/internal/deadcode"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/pkg/client"
)

// codeIntelStubBackend implements Backend with injectable code-intel responses.
type codeIntelStubBackend struct {
	stubBackend
	callersFunc  func(ctx context.Context, project, file string, line int) (*codeintel.CallersResult, error)
	explainFunc  func(ctx context.Context, project, file string, line int) (*codeintel.ExplainResult, error)
	impactFunc   func(ctx context.Context, project, file string, line int, depth int) (*codeintel.ImpactResult, error)
	deadCodeFunc func(ctx context.Context, project string) (*codeintel.DeadCodeResult, error)
	diffFunc     func(ctx context.Context, refRange string) (*codeintel.DiffResult, error)
}

func (b *codeIntelStubBackend) Callers(ctx context.Context, project, file string, line int) (*codeintel.CallersResult, error) {
	if b.callersFunc != nil {
		return b.callersFunc(ctx, project, file, line)
	}
	return b.stubBackend.Callers(ctx, project, file, line)
}
func (b *codeIntelStubBackend) Explain(ctx context.Context, project, file string, line int) (*codeintel.ExplainResult, error) {
	if b.explainFunc != nil {
		return b.explainFunc(ctx, project, file, line)
	}
	return b.stubBackend.Explain(ctx, project, file, line)
}
func (b *codeIntelStubBackend) Impact(ctx context.Context, project, file string, line int, depth int) (*codeintel.ImpactResult, error) {
	if b.impactFunc != nil {
		return b.impactFunc(ctx, project, file, line, depth)
	}
	return b.stubBackend.Impact(ctx, project, file, line, depth)
}
func (b *codeIntelStubBackend) DeadCode(ctx context.Context, project string) (*codeintel.DeadCodeResult, error) {
	if b.deadCodeFunc != nil {
		return b.deadCodeFunc(ctx, project)
	}
	return b.stubBackend.DeadCode(ctx, project)
}
func (b *codeIntelStubBackend) Diff(ctx context.Context, refRange string) (*codeintel.DiffResult, error) {
	if b.diffFunc != nil {
		return b.diffFunc(ctx, refRange)
	}
	return b.stubBackend.Diff(ctx, refRange)
}

func connectCodeIntel(t *testing.T, b Backend) *mcp.ClientSession {
	t.Helper()
	server := New(b)
	serverT, clientT := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	cli := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	sess, err := cli.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func TestCodeIntelToolsRegistered(t *testing.T) {
	sess := connectCodeIntel(t, &codeIntelStubBackend{
		stubBackend: stubBackend{
			projectsFunc: func(context.Context) ([]ProjectInfo, error) { return nil, nil },
			searchFunc: func(context.Context, string, string, string, int, bool, int) (*SearchOutput, error) {
				return &SearchOutput{}, nil
			},
			reindexFunc: func(context.Context, string, string) (string, error) { return "", nil },
			statusFunc: func(context.Context, string) (*StatusInfo, error) {
				return &StatusInfo{Name: "x"}, nil
			},
		},
	})
	res, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for _, want := range []string{
		toolSemanticCallers, toolSemanticExplain, toolSemanticImpact,
		toolSemanticDeadCode, toolSemanticDiff,
	} {
		if !got[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

func TestSemanticCallersHappyPath(t *testing.T) {
	b := &codeIntelStubBackend{
		stubBackend: stubBackend{
			projectsFunc: func(context.Context) ([]ProjectInfo, error) { return nil, nil },
			searchFunc: func(context.Context, string, string, string, int, bool, int) (*SearchOutput, error) {
				return &SearchOutput{}, nil
			},
			reindexFunc: func(context.Context, string, string) (string, error) { return "", nil },
			statusFunc:  func(context.Context, string) (*StatusInfo, error) { return &StatusInfo{}, nil },
		},
		callersFunc: func(_ context.Context, project, file string, line int) (*codeintel.CallersResult, error) {
			if project != "app" || file != "internal/auth/token.go" || line != 42 {
				t.Errorf("Callers args = (%q,%q,%d)", project, file, line)
			}
			return &codeintel.CallersResult{
				Symbol:     &analyzer.Symbol{Name: "ValidateToken", Kind: "function", StartLine: 42, EndLine: 50},
				Direct:     []string{"internal/api/api.go", "cmd/main.go"},
				Transitive: []string{"internal/web/web.go"},
			}, nil
		},
	}
	sess := connectCodeIntel(t, b)
	text, isErr := callText(t, sess, toolSemanticCallers, map[string]any{
		"project": "app", "file": "internal/auth/token.go", "line": 42,
	})
	if isErr {
		t.Fatalf("unexpected error: %q", text)
	}
	if !strings.Contains(text, "ValidateToken") {
		t.Errorf("missing symbol: %q", text)
	}
	if !strings.Contains(text, "internal/api/api.go") || !strings.Contains(text, "internal/web/web.go") {
		t.Errorf("missing callers: %q", text)
	}
}

func TestSemanticDeadCodeHappyPath(t *testing.T) {
	b := &codeIntelStubBackend{
		stubBackend: stubBackend{
			projectsFunc: func(context.Context) ([]ProjectInfo, error) { return nil, nil },
			searchFunc: func(context.Context, string, string, string, int, bool, int) (*SearchOutput, error) {
				return &SearchOutput{}, nil
			},
			reindexFunc: func(context.Context, string, string) (string, error) { return "", nil },
			statusFunc:  func(context.Context, string) (*StatusInfo, error) { return &StatusInfo{}, nil },
		},
		deadCodeFunc: func(_ context.Context, project string) (*codeintel.DeadCodeResult, error) {
			if project != "app" {
				t.Errorf("project = %q", project)
			}
			return &codeintel.DeadCodeResult{
				Findings: []deadcode.Finding{
					{Symbol: "unusedHelper", Kind: "function", File: "internal/util/old.go", StartLine: 10, Confidence: "confirmed"},
					{Symbol: "LegacyAPI", Kind: "function", File: "pkg/api/legacy.go", StartLine: 5, Confidence: "public-api"},
				},
				Stats: deadcode.Stats{TotalFindings: 2, Confirmed: 1, PublicAPI: 1},
			}, nil
		},
	}
	sess := connectCodeIntel(t, b)
	text, isErr := callText(t, sess, toolSemanticDeadCode, map[string]any{"project": "app"})
	if isErr {
		t.Fatalf("unexpected error: %q", text)
	}
	if !strings.Contains(text, "unusedHelper") || !strings.Contains(text, "LegacyAPI") {
		t.Errorf("missing findings: %q", text)
	}
	if !strings.Contains(text, "Confirmed dead") || !strings.Contains(text, "Total dead: 2") {
		t.Errorf("missing summary: %q", text)
	}
}

func TestSemanticDiffParseAndFormat(t *testing.T) {
	b := &codeIntelStubBackend{
		stubBackend: stubBackend{
			projectsFunc: func(context.Context) ([]ProjectInfo, error) { return nil, nil },
			searchFunc: func(context.Context, string, string, string, int, bool, int) (*SearchOutput, error) {
				return &SearchOutput{}, nil
			},
			reindexFunc: func(context.Context, string, string) (string, error) { return "", nil },
			statusFunc:  func(context.Context, string) (*StatusInfo, error) { return &StatusInfo{}, nil },
		},
		diffFunc: func(_ context.Context, refRange string) (*codeintel.DiffResult, error) {
			if refRange != "main..feat/x" {
				t.Errorf("refRange = %q", refRange)
			}
			return &codeintel.DiffResult{
				Ref1: "main", Ref2: "feat/x",
				New:     []codeintel.SymbolDiff{{Name: "NewFunc", FilePath: "a.go", Line: 3, Kind: "func"}},
				Removed: []codeintel.SymbolDiff{{Name: "OldFunc", FilePath: "b.go", Line: 1, Kind: "func"}},
				Changed: []codeintel.SymbolDiff{{
					Name: "Tweaked", FilePath: "c.go", Line: 9, Kind: "func",
					OldSignature: "func Tweaked()", Signature: "func Tweaked(x int)",
				}},
			}, nil
		},
	}
	sess := connectCodeIntel(t, b)
	text, isErr := callText(t, sess, toolSemanticDiff, map[string]any{"ref_range": "main..feat/x"})
	if isErr {
		t.Fatalf("unexpected error: %q", text)
	}
	if !strings.Contains(text, "main → feat/x") {
		t.Errorf("missing refs: %q", text)
	}
	if !strings.Contains(text, "+ NewFunc") || !strings.Contains(text, "- OldFunc") || !strings.Contains(text, "~ Tweaked") {
		t.Errorf("missing symbol diffs: %q", text)
	}

	// empty ref_range
	errText, isErr := callText(t, sess, toolSemanticDiff, map[string]any{"ref_range": ""})
	if !isErr {
		t.Fatalf("expected error for empty ref_range; got %q", errText)
	}
	if !strings.Contains(errText, "ref_range") {
		t.Errorf("error should mention ref_range: %q", errText)
	}
}

func TestSemanticExplainAndImpact(t *testing.T) {
	b := &codeIntelStubBackend{
		stubBackend: stubBackend{
			projectsFunc: func(context.Context) ([]ProjectInfo, error) { return nil, nil },
			searchFunc: func(context.Context, string, string, string, int, bool, int) (*SearchOutput, error) {
				return &SearchOutput{}, nil
			},
			reindexFunc: func(context.Context, string, string) (string, error) { return "", nil },
			statusFunc:  func(context.Context, string) (*StatusInfo, error) { return &StatusInfo{}, nil },
		},
		explainFunc: func(_ context.Context, _, _ string, _ int) (*codeintel.ExplainResult, error) {
			return &codeintel.ExplainResult{
				Display:   "auth.ValidateToken",
				File:      "internal/auth/token.go",
				Symbol:    &analyzer.Symbol{Name: "ValidateToken", Kind: "function", StartLine: 42, EndLine: 50},
				Imports:   []string{"fmt", "strings"},
				Importers: []string{"internal/api/api.go"},
				Tests:     []string{"internal/auth/token_test.go"},
			}, nil
		},
		impactFunc: func(_ context.Context, _, _ string, _ int, depth int) (*codeintel.ImpactResult, error) {
			if depth != 3 {
				t.Errorf("depth = %d, want 3", depth)
			}
			return &codeintel.ImpactResult{
				Symbol: &analyzer.Symbol{Name: "ValidateToken"},
				Affected: []codeintel.ImpactNode{
					{File: "internal/api/api.go", Depth: 1},
					{File: "internal/web/web.go", Depth: 2},
				},
				TotalCount: 2,
			}, nil
		},
	}
	sess := connectCodeIntel(t, b)

	text, isErr := callText(t, sess, toolSemanticExplain, map[string]any{
		"project": "app", "file": "internal/auth/token.go", "line": 42,
	})
	if isErr {
		t.Fatalf("explain error: %q", text)
	}
	if !strings.Contains(text, "auth.ValidateToken") || !strings.Contains(text, "token_test.go") {
		t.Errorf("explain text incomplete: %q", text)
	}

	text, isErr = callText(t, sess, toolSemanticImpact, map[string]any{
		"project": "app", "file": "internal/auth/token.go", "line": 42, "depth": 3,
	})
	if isErr {
		t.Fatalf("impact error: %q", text)
	}
	if !strings.Contains(text, "[d=1] internal/api/api.go") || !strings.Contains(text, "Affected files: 2") {
		t.Errorf("impact text incomplete: %q", text)
	}
}

func TestCodeIntelValidationErrors(t *testing.T) {
	b := &codeIntelStubBackend{
		stubBackend: stubBackend{
			projectsFunc: func(context.Context) ([]ProjectInfo, error) { return nil, nil },
			searchFunc: func(context.Context, string, string, string, int, bool, int) (*SearchOutput, error) {
				return &SearchOutput{}, nil
			},
			reindexFunc: func(context.Context, string, string) (string, error) { return "", nil },
			statusFunc:  func(context.Context, string) (*StatusInfo, error) { return &StatusInfo{}, nil },
		},
	}
	sess := connectCodeIntel(t, b)

	text, isErr := callText(t, sess, toolSemanticCallers, map[string]any{
		"project": "app", "file": "", "line": 1,
	})
	if !isErr || !strings.Contains(text, "file is required") {
		t.Errorf("expected file required error; got isErr=%v text=%q", isErr, text)
	}

	text, isErr = callText(t, sess, toolSemanticImpact, map[string]any{
		"project": "app", "file": "a.go", "line": 0,
	})
	if !isErr || !strings.Contains(text, "line must be") {
		t.Errorf("expected line error; got isErr=%v text=%q", isErr, text)
	}
}

func TestCodeIntelRemoteNotImplemented(t *testing.T) {
	httpSrv := stubServer(t)
	b := NewClientBackend(client.New(httpSrv.URL, "tok"))
	sess := connectCodeIntel(t, b)

	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{toolSemanticCallers, map[string]any{"project": "app", "file": "a.go", "line": 1}},
		{toolSemanticExplain, map[string]any{"project": "app", "file": "a.go", "line": 1}},
		{toolSemanticImpact, map[string]any{"project": "app", "file": "a.go", "line": 1}},
		{toolSemanticDeadCode, map[string]any{"project": "app"}},
		{toolSemanticDiff, map[string]any{"ref_range": "main..HEAD"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, isErr := callText(t, sess, tc.name, tc.args)
			if !isErr {
				t.Fatalf("expected isError; text=%q", text)
			}
			if !strings.Contains(text, "standalone/local mode only") {
				t.Errorf("error missing standalone message: %q", text)
			}
			if !strings.Contains(text, tc.name) {
				t.Errorf("error missing tool name %q: %q", tc.name, text)
			}
		})
	}
}

func TestFormatHelpers_Empty(t *testing.T) {
	if s := formatCallers(&codeintel.CallersResult{}); !strings.Contains(s, "(unknown)") {
		t.Errorf("formatCallers empty: %q", s)
	}
	if s := formatExplain(&codeintel.ExplainResult{Display: "x", File: "a.go"}); !strings.Contains(s, "x (a.go)") {
		t.Errorf("formatExplain empty symbol: %q", s)
	}
	if s := formatImpact(&codeintel.ImpactResult{}); !strings.Contains(s, "Affected files: 0") {
		t.Errorf("formatImpact empty: %q", s)
	}
	if s := formatDeadCode(&codeintel.DeadCodeResult{}); s != "No dead code found." {
		t.Errorf("formatDeadCode empty: %q", s)
	}
	if s := formatDiff(&codeintel.DiffResult{Ref1: "a", Ref2: "b"}); !strings.Contains(s, "No semantic differences") {
		t.Errorf("formatDiff empty: %q", s)
	}
}

func TestLocalBackendDiffParseError(t *testing.T) {
	b := &localBackend{}
	_, err := b.Diff(context.Background(), "not-a-range")
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "invalid ref range") {
		t.Errorf("err = %v", err)
	}
}

func TestLocalBackendCodeIntelResolveError(t *testing.T) {
	fs := &failIndexStore{}
	b := &localBackend{idx: fs, caps: agent.Capabilities{}}

	if _, err := b.Callers(context.Background(), "", "a.go", 1); err == nil {
		t.Error("Callers expected resolve error")
	}
	if _, err := b.Explain(context.Background(), "missing", "a.go", 1); err == nil {
		t.Error("Explain expected resolve error")
	}
	if _, err := b.Impact(context.Background(), "missing", "a.go", 1, 2); err == nil {
		t.Error("Impact expected resolve error")
	}
	if _, err := b.DeadCode(context.Background(), "missing"); err == nil {
		t.Error("DeadCode expected resolve error")
	}
}

// failIndexStore embeds IndexStore and fails project resolution.
type failIndexStore struct {
	store.IndexStore
}

func (f *failIndexStore) GetProjectByIdentity(context.Context, string) (*store.Project, error) {
	return nil, errors.New("not found")
}

func (f *failIndexStore) GetProject(context.Context, string) (*store.Project, error) {
	return nil, errors.New("not found")
}

func (f *failIndexStore) ListProjects(context.Context, int, int) ([]store.Project, error) {
	return nil, errors.New("list failed")
}

func TestLocalBackendCallersHappyPath(t *testing.T) {
	tmp := t.TempDir()
	// write a simple Go file at project root
	src := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc Hello() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	proj := &store.Project{ID: 1, Name: "p", Path: tmp, Identity: "id-p"}
	fs := &ciLocalStore{
		project: proj,
		graph: map[string][]string{
			"cmd/cli.go": {"./"},
		},
	}
	b := &localBackend{idx: fs}
	res, err := b.Callers(context.Background(), "p", "main.go", 3)
	if err != nil {
		t.Fatalf("Callers: %v", err)
	}
	if res.Symbol == nil || res.Symbol.Name != "Hello" {
		t.Fatalf("symbol = %v", res.Symbol)
	}
	if len(res.Direct) != 1 || res.Direct[0] != "cmd/cli.go" {
		t.Fatalf("direct = %v", res.Direct)
	}

	exp, err := b.Explain(context.Background(), "p", "main.go", 3)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if exp.Symbol == nil || exp.Symbol.Name != "Hello" {
		t.Fatalf("explain symbol = %v", exp.Symbol)
	}

	imp, err := b.Impact(context.Background(), "p", "main.go", 3, 2)
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	if imp.TotalCount != 1 {
		t.Fatalf("impact count = %d", imp.TotalCount)
	}

	// DeadCode needs ListFileHashes; our store returns the main file.
	dc, err := b.DeadCode(context.Background(), "p")
	if err != nil {
		t.Fatalf("DeadCode: %v", err)
	}
	if dc == nil {
		t.Fatal("nil deadcode result")
	}
}

func TestExplainHandlerBackendError(t *testing.T) {
	b := &codeIntelStubBackend{
		stubBackend: stubBackend{
			projectsFunc: func(context.Context) ([]ProjectInfo, error) { return nil, nil },
			searchFunc: func(context.Context, string, string, string, int, bool, int) (*SearchOutput, error) {
				return &SearchOutput{}, nil
			},
			reindexFunc: func(context.Context, string, string) (string, error) { return "", nil },
			statusFunc:  func(context.Context, string) (*StatusInfo, error) { return &StatusInfo{}, nil },
		},
		explainFunc: func(context.Context, string, string, int) (*codeintel.ExplainResult, error) {
			return nil, errors.New("boom")
		},
		callersFunc: func(context.Context, string, string, int) (*codeintel.CallersResult, error) {
			return nil, errors.New("boom")
		},
		impactFunc: func(context.Context, string, string, int, int) (*codeintel.ImpactResult, error) {
			return nil, errors.New("boom")
		},
		deadCodeFunc: func(context.Context, string) (*codeintel.DeadCodeResult, error) {
			return nil, errors.New("boom")
		},
		diffFunc: func(context.Context, string) (*codeintel.DiffResult, error) {
			return nil, errors.New("boom")
		},
	}
	sess := connectCodeIntel(t, b)
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{toolSemanticCallers, map[string]any{"project": "p", "file": "a.go", "line": 1}},
		{toolSemanticExplain, map[string]any{"project": "p", "file": "a.go", "line": 1}},
		{toolSemanticImpact, map[string]any{"project": "p", "file": "a.go", "line": 1}},
		{toolSemanticDeadCode, map[string]any{"project": "p"}},
		{toolSemanticDiff, map[string]any{"ref_range": "a..b"}},
	} {
		text, isErr := callText(t, sess, tc.name, tc.args)
		if !isErr || !strings.Contains(text, "boom") {
			t.Errorf("%s: want boom error, got isErr=%v text=%q", tc.name, isErr, text)
		}
	}
}

// ciLocalStore is a minimal IndexStore for localBackend codeintel happy paths.
type ciLocalStore struct {
	store.IndexStore
	project *store.Project
	graph   map[string][]string
}

func (s *ciLocalStore) GetProject(_ context.Context, name string) (*store.Project, error) {
	if s.project != nil && (name == s.project.Name || name == "") {
		return s.project, nil
	}
	return nil, errors.New("not found")
}
func (s *ciLocalStore) GetProjectByIdentity(_ context.Context, id string) (*store.Project, error) {
	if s.project != nil && id == s.project.Identity {
		return s.project, nil
	}
	return nil, errors.New("not found")
}
func (s *ciLocalStore) ListProjects(context.Context, int, int) ([]store.Project, error) {
	if s.project == nil {
		return nil, nil
	}
	return []store.Project{*s.project}, nil
}
func (s *ciLocalStore) FetchGraphNeighbors(context.Context, int) (map[string][]string, error) {
	return s.graph, nil
}
func (s *ciLocalStore) ListFileHashes(context.Context, int) (map[string]string, error) {
	return map[string]string{"main.go": "abc"}, nil
}

func TestLocalBackendDiffHappyPath(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("git", "init", "-q")
	run("git", "config", "user.email", "t@t.com")
	run("git", "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc Old() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("git", "add", ".")
	run("git", "commit", "-q", "--no-verify", "-m", "init")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc Old() {}\nfunc New() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("git", "add", ".")
	run("git", "commit", "-q", "--no-verify", "-m", "add new")

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	b := &localBackend{}
	res, err := b.Diff(context.Background(), "HEAD~1..HEAD")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(res.New) == 0 {
		t.Fatalf("expected new symbols, got %+v", res)
	}
}

func TestCodeIntelAllowlistSkipsTools(t *testing.T) {
	b := &codeIntelStubBackend{
		stubBackend: stubBackend{
			projectsFunc: func(context.Context) ([]ProjectInfo, error) { return nil, nil },
			searchFunc: func(context.Context, string, string, string, int, bool, int) (*SearchOutput, error) {
				return &SearchOutput{}, nil
			},
			reindexFunc: func(context.Context, string, string) (string, error) { return "", nil },
			statusFunc:  func(context.Context, string) (*StatusInfo, error) { return &StatusInfo{}, nil },
		},
	}
	sess := connectWithOptions(t, b, Options{AllowedTools: []string{toolSemanticSearch, toolSemanticProjects}})
	res, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		switch tool.Name {
		case toolSemanticCallers, toolSemanticExplain, toolSemanticImpact, toolSemanticDeadCode, toolSemanticDiff:
			t.Errorf("tool %s should be filtered by allowlist", tool.Name)
		}
	}
}

func TestFormatExplainWithSymbol(t *testing.T) {
	s := formatExplain(&codeintel.ExplainResult{
		Display:   "pkg.F",
		File:      "f.go",
		Symbol:    &analyzer.Symbol{Name: "F", Kind: "function", StartLine: 1, EndLine: 2},
		Imports:   []string{"fmt"},
		Importers: []string{"a.go"},
		Tests:     []string{"f_test.go"},
	})
	if !strings.Contains(s, "pkg.F — function (f.go:1-2)") {
		t.Errorf("format: %q", s)
	}
}

func TestFormatImpactWithSymbolNone(t *testing.T) {
	s := formatImpact(&codeintel.ImpactResult{Symbol: &analyzer.Symbol{Name: "X"}, TotalCount: 0})
	if !strings.Contains(s, "Impact of changing: X") || !strings.Contains(s, "none") {
		t.Errorf("%q", s)
	}
}

func TestFormatCallersNoTransitive(t *testing.T) {
	s := formatCallers(&codeintel.CallersResult{
		Symbol: &analyzer.Symbol{Name: "F"},
		Direct: []string{"a.go"},
	})
	if strings.Contains(s, "Transitive") {
		t.Errorf("unexpected transitive: %q", s)
	}
}

func TestFormatDeadCodeConfirmedOnly(t *testing.T) {
	s := formatDeadCode(&codeintel.DeadCodeResult{
		Findings: []deadcode.Finding{{Symbol: "x", Kind: "func", File: "a.go", StartLine: 1, Confidence: "confirmed"}},
		Stats:    deadcode.Stats{TotalFindings: 1, Confirmed: 1},
	})
	if !strings.Contains(s, "Confirmed dead") || strings.Contains(s, "Likely dead") {
		t.Errorf("%q", s)
	}
}

func TestFormatDiffPartialSections(t *testing.T) {
	s := formatDiff(&codeintel.DiffResult{
		Ref1: "a", Ref2: "b",
		New: []codeintel.SymbolDiff{{Name: "N", FilePath: "n.go", Line: 1, Kind: "func"}},
	})
	if !strings.Contains(s, "+ N") || strings.Contains(s, "Removed") {
		t.Errorf("%q", s)
	}
}

func TestMultiSearchHandlerViaMCP(t *testing.T) {
	b := &multiSearchStub{
		stubBackend: stubBackend{
			projectsFunc: func(context.Context) ([]ProjectInfo, error) { return nil, nil },
			searchFunc: func(context.Context, string, string, string, int, bool, int) (*SearchOutput, error) {
				return &SearchOutput{}, nil
			},
			reindexFunc: func(context.Context, string, string) (string, error) { return "", nil },
			statusFunc:  func(context.Context, string) (*StatusInfo, error) { return &StatusInfo{}, nil },
		},
	}
	sess := connectCodeIntel(t, b)
	text, isErr := callText(t, sess, toolSemanticSearchMulti, map[string]any{
		"query": "auth", "identities": []string{"id1"}, "all": false,
	})
	if isErr {
		t.Fatalf("unexpected error: %q", text)
	}
	// error path
	b.err = errors.New("multi fail")
	text, isErr = callText(t, sess, toolSemanticSearchMulti, map[string]any{
		"query": "x", "identities": []string{"id1"},
	})
	if !isErr || !strings.Contains(text, "multi fail") {
		t.Errorf("want multi fail, got isErr=%v text=%q", isErr, text)
	}
}

type multiSearchStub struct {
	stubBackend
	err error
}

func (m *multiSearchStub) SearchMulti(_ context.Context, req search.MultiScopeRequest) (*search.MultiResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &search.MultiResponse{ProjectCount: 1}, nil
}
