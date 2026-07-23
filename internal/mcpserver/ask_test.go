package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/codeintel"
	"github.com/lgldsilva/semidx/pkg/client"
)

// fakeAskBackend implements both Backend and AskBackend for testing.
type fakeAskBackend struct {
	answer *AskOutput
	err    error
}

func (f *fakeAskBackend) Search(_ context.Context, _, _, _ string, _ int, _ bool, _ int) (*SearchOutput, error) {
	return &SearchOutput{}, nil
}

func (f *fakeAskBackend) Projects(_ context.Context) ([]ProjectInfo, error) {
	return nil, nil
}

func (f *fakeAskBackend) Reindex(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

func (f *fakeAskBackend) Status(_ context.Context, _ string) (*StatusInfo, error) {
	return &StatusInfo{Name: "test", TotalFiles: 0, Status: "ready", Model: "test"}, nil
}

func (f *fakeAskBackend) Capabilities() agent.Capabilities {
	return agent.Capabilities{}
}

func (f *fakeAskBackend) Callers(context.Context, string, string, int) (*codeintel.CallersResult, error) {
	return &codeintel.CallersResult{}, nil
}
func (f *fakeAskBackend) Explain(context.Context, string, string, int) (*codeintel.ExplainResult, error) {
	return &codeintel.ExplainResult{}, nil
}
func (f *fakeAskBackend) Impact(context.Context, string, string, int, int) (*codeintel.ImpactResult, error) {
	return &codeintel.ImpactResult{}, nil
}
func (f *fakeAskBackend) DeadCode(context.Context, string) (*codeintel.DeadCodeResult, error) {
	return &codeintel.DeadCodeResult{}, nil
}
func (f *fakeAskBackend) Diff(context.Context, string) (*codeintel.DiffResult, error) {
	return &codeintel.DiffResult{}, nil
}

func (f *fakeAskBackend) Ask(_ context.Context, _, _ string, _ int) (*AskOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.answer, nil
}

// compile-time checks.
var _ AskBackend = (*fakeAskBackend)(nil)

// connectAskBackend wires an in-memory MCP session for the given Backend.
func connectAskBackend(t *testing.T, b Backend) *mcp.ClientSession {
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

func TestSemanticAskRegistersWithAskBackend(t *testing.T) {
	b := &fakeAskBackend{
		answer: &AskOutput{Answer: "test", Model: "test-model"},
	}
	sess := connectAskBackend(t, b)
	res, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range res.Tools {
		if tool.Name == "semantic_ask" {
			found = true
			break
		}
	}
	if !found {
		t.Error("semantic_ask tool should be registered when backend implements AskBackend")
	}
}

func TestSemanticAskNotRegisteredWithPlainBackend(t *testing.T) {
	httpSrv := stubServer(t)
	b := NewClientBackend(client.New(httpSrv.URL, "tok"))
	sess := connectAskBackend(t, b)
	res, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name == "semantic_ask" {
			t.Error("semantic_ask tool should NOT be registered with a plain Backend")
		}
	}
}

func TestSemanticAskReturnsFormattedAnswer(t *testing.T) {
	b := &fakeAskBackend{
		answer: &AskOutput{
			Answer: "The function Foo returns 42.",
			Sources: []AskSource{
				{Path: "internal/foo.go", StartLine: 10, EndLine: 12, Score: 0.95},
				{Path: "internal/bar.go", StartLine: 5, EndLine: 8, Score: 0.85, Keyword: true},
			},
			Model:    "gemini-2.5-flash",
			Fallback: false,
		},
	}
	sess := connectAskBackend(t, b)
	text, isErr := callText(t, sess, "semantic_ask", map[string]any{
		"project":  "myproject",
		"question": "What does Foo do?",
	})
	if isErr {
		t.Fatalf("unexpected isError; text=%q", text)
	}
	if !strings.Contains(text, "Foo returns 42") {
		t.Errorf("answer missing content: %q", text)
	}
	if !strings.Contains(text, "internal/foo.go:10-12") {
		t.Errorf("answer missing source reference: %q", text)
	}
	if !strings.Contains(text, "0.950") || !strings.Contains(text, "0.850") {
		t.Errorf("answer missing scores: %q", text)
	}
	if !strings.Contains(text, "gemini-2.5-flash") {
		t.Errorf("answer missing model: %q", text)
	}
	if !strings.Contains(text, "[keyword]") {
		t.Errorf("answer missing keyword annotation for keyword source: %q", text)
	}
}

func TestSemanticAskFallbackFlag(t *testing.T) {
	b := &fakeAskBackend{
		answer: &AskOutput{
			Answer:   "Keyword-based answer.",
			Sources:  []AskSource{{Path: "a.go", StartLine: 1, EndLine: 1, Score: 0.5}},
			Model:    "gemini-2.5-flash",
			Fallback: true,
		},
	}
	sess := connectAskBackend(t, b)
	text, isErr := callText(t, sess, "semantic_ask", map[string]any{
		"project":  "myproject",
		"question": "test",
	})
	if isErr {
		t.Fatalf("unexpected isError; text=%q", text)
	}
	if !strings.Contains(text, "[keyword fallback]") {
		t.Errorf("answer missing fallback annotation: %q", text)
	}
}

func TestSemanticAskError(t *testing.T) {
	b := &fakeAskBackend{
		err: errAskFailed,
	}
	sess := connectAskBackend(t, b)
	text, isErr := callText(t, sess, "semantic_ask", map[string]any{
		"project":  "myproject",
		"question": "test",
	})
	if !isErr {
		t.Errorf("expected isError=true for failing AskBackend; text=%q", text)
	}
	if !strings.Contains(text, "RAG pipeline failed") {
		t.Errorf("error text = %q, want 'RAG pipeline failed'", text)
	}
}

// errAskFailed is a sentinel error for Ask failure tests.
var errAskFailed = &askError{"RAG pipeline failed"}

type askError struct{ msg string }

func (e *askError) Error() string { return e.msg }

func TestFormatAsk(t *testing.T) {
	t.Run("with sources", func(t *testing.T) {
		out := &AskOutput{
			Answer: "The answer.",
			Sources: []AskSource{
				{Path: "a.go", StartLine: 1, EndLine: 5, Score: 0.912},
				{Path: "b.go", StartLine: 10, EndLine: 20, Score: 0.5, Keyword: true},
			},
			Model:    "gpt-4",
			Fallback: false,
		}
		got := formatAsk(out)
		if !strings.Contains(got, "The answer.") {
			t.Errorf("missing answer content: %q", got)
		}
		if !strings.Contains(got, "a.go:1-5 (0.912)") {
			t.Errorf("missing source line: %q", got)
		}
		if !strings.Contains(got, "b.go:10-20 (0.500) [keyword]") {
			t.Errorf("missing keyword source line: %q", got)
		}
		if !strings.Contains(got, "Model: gpt-4") {
			t.Errorf("missing model: %q", got)
		}
	})

	t.Run("with fallback", func(t *testing.T) {
		out := &AskOutput{
			Answer:   "Fallback answer.",
			Sources:  []AskSource{{Path: "a.go", StartLine: 1, EndLine: 1, Score: 0.3}},
			Model:    "gpt-4",
			Fallback: true,
		}
		got := formatAsk(out)
		if !strings.Contains(got, "[keyword fallback]") {
			t.Errorf("missing fallback annotation: %q", got)
		}
	})

	t.Run("without sources", func(t *testing.T) {
		out := &AskOutput{
			Answer: "No context answer.",
			Model:  "gpt-4",
		}
		got := formatAsk(out)
		if !strings.Contains(got, "No context answer.") {
			t.Errorf("missing answer: %q", got)
		}
		if strings.Contains(got, "Sources:") {
			t.Errorf("unexpected Sources section: %q", got)
		}
	})

	t.Run("trailing newline trimmed", func(t *testing.T) {
		out := &AskOutput{
			Answer:   "Trimmed.",
			Sources:  []AskSource{{Path: "a.go", StartLine: 1, EndLine: 1, Score: 0.5}},
			Model:    "m",
			Fallback: false,
		}
		got := formatAsk(out)
		if len(got) > 0 && got[len(got)-1] == '\n' {
			t.Errorf("formatAsk should not end with newline: %q", got)
		}
	})
}
