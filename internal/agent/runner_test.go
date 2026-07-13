package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"charm.land/fantasy"
)

// fakeModel is a minimal fantasy.LanguageModel that replays pre-programmed
// Generate responses. Only Generate is exercised by Runner; the object/stream
// methods are stubs.
type fakeModel struct {
	responses   []*fantasy.Response
	idx         int
	gotTools    []fantasy.Tool
	streamParts []fantasy.StreamPart // replayed by Stream, in order
}

func (f *fakeModel) Generate(_ context.Context, call fantasy.Call) (*fantasy.Response, error) {
	f.gotTools = call.Tools
	if f.idx >= len(f.responses) {
		return &fantasy.Response{
			Content:      fantasy.ResponseContent{fantasy.TextContent{Text: "done"}},
			FinishReason: fantasy.FinishReasonStop,
		}, nil
	}
	r := f.responses[f.idx]
	f.idx++
	return r, nil
}

func (f *fakeModel) Stream(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	f.gotTools = call.Tools
	parts := f.streamParts
	return func(yield func(fantasy.StreamPart) bool) {
		for _, p := range parts {
			if !yield(p) {
				return
			}
		}
	}, nil
}

func (f *fakeModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeModel) Provider() string { return "fake" }
func (f *fakeModel) Model() string    { return "fake-model" }

func TestRunner_plainText(t *testing.T) {
	fm := &fakeModel{responses: []*fantasy.Response{
		{
			Content:      fantasy.ResponseContent{fantasy.TextContent{Text: "hello world"}},
			FinishReason: fantasy.FinishReasonStop,
		},
	}}
	r := NewRunner(fm, nil, RunnerConfig{SystemPrompt: "be brief"})

	ans, err := r.Ask(context.Background(), "hi", nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if ans.Content != "hello world" {
		t.Errorf("Content = %q, want %q", ans.Content, "hello world")
	}
	if len(ans.Trace) != 0 {
		t.Errorf("trace should be empty for a no-tool answer, got %v", ans.Trace)
	}
	if ans.Model != "fake-model" {
		t.Errorf("Model = %q, want fake-model", ans.Model)
	}
}

type echoInput struct {
	Q string `json:"q" description:"query"`
}

// echoTool is a minimal parallel tool that returns a fixed textual response.
func echoTool(result string) fantasy.AgentTool {
	return fantasy.NewParallelAgentTool("echo", "echo tool",
		func(_ context.Context, _ echoInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			return fantasy.NewTextResponse(result), nil
		})
}

// errTool returns a soft (non-critical) error response.
func errTool(msg string) fantasy.AgentTool {
	return fantasy.NewParallelAgentTool("echo", "echo tool",
		func(_ context.Context, _ echoInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			return fantasy.NewTextErrorResponse(msg), nil
		})
}

// toolThenText programs a fakeModel to call the echo tool once, then answer.
func toolThenText() *fakeModel {
	return &fakeModel{responses: []*fantasy.Response{
		{
			Content: fantasy.ResponseContent{
				fantasy.ToolCallContent{ToolCallID: "c1", ToolName: "echo", Input: `{"q":"x"}`},
			},
			FinishReason: fantasy.FinishReasonToolCalls,
			Usage:        fantasy.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		},
		{
			Content:      fantasy.ResponseContent{fantasy.TextContent{Text: "final answer"}},
			FinishReason: fantasy.FinishReasonStop,
			Usage:        fantasy.Usage{InputTokens: 20, OutputTokens: 8, TotalTokens: 28},
		},
	}}
}

func TestRunner_toolCallTraceUsageAndMessages(t *testing.T) {
	r := NewRunner(toolThenText(), []fantasy.AgentTool{echoTool(`{"results":[]}`)}, RunnerConfig{})
	ans, err := r.Ask(context.Background(), "hi", nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if ans.Content != "final answer" {
		t.Errorf("Content = %q, want %q", ans.Content, "final answer")
	}

	// Trace carries the call args AND the real tool result.
	if len(ans.Trace) != 1 {
		t.Fatalf("want 1 trace record, got %d: %+v", len(ans.Trace), ans.Trace)
	}
	tc := ans.Trace[0]
	if tc.Tool != "echo" || tc.Args != `{"q":"x"}` {
		t.Errorf("trace call = %+v", tc)
	}
	if tc.Result != `{"results":[]}` {
		t.Errorf("trace Result = %q, want the tool output", tc.Result)
	}
	if tc.Error != "" {
		t.Errorf("trace Error should be empty, got %q", tc.Error)
	}

	// Usage aggregates across both steps.
	if ans.Usage.InputTokens != 30 || ans.Usage.OutputTokens != 13 || ans.Usage.TotalTokens != 43 {
		t.Errorf("Usage = %+v, want in=30 out=13 total=43", ans.Usage)
	}

	// Messages record the full turn: assistant(tool_call) + tool(result) +
	// assistant(text). This is what feeds multi-turn tool memory.
	if len(ans.Messages) != 3 {
		t.Fatalf("want 3 turn messages, got %d", len(ans.Messages))
	}
	wantRoles := []fantasy.MessageRole{
		fantasy.MessageRoleAssistant, fantasy.MessageRoleTool, fantasy.MessageRoleAssistant,
	}
	for i, want := range wantRoles {
		if ans.Messages[i].Role != want {
			t.Errorf("message[%d].Role = %q, want %q", i, ans.Messages[i].Role, want)
		}
	}
}

func TestRunner_toolErrorCapturedInTrace(t *testing.T) {
	r := NewRunner(toolThenText(), []fantasy.AgentTool{errTool("boom")}, RunnerConfig{})
	ans, err := r.Ask(context.Background(), "hi", nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(ans.Trace) != 1 {
		t.Fatalf("want 1 trace record, got %d", len(ans.Trace))
	}
	if ans.Trace[0].Error != "boom" {
		t.Errorf("trace Error = %q, want %q", ans.Trace[0].Error, "boom")
	}
	if ans.Trace[0].Result != "" {
		t.Errorf("trace Result should be empty on error, got %q", ans.Trace[0].Result)
	}
}

func TestRunner_streamTextDeltas(t *testing.T) {
	fm := &fakeModel{streamParts: []fantasy.StreamPart{
		{Type: fantasy.StreamPartTypeTextStart, ID: "t1"},
		{Type: fantasy.StreamPartTypeTextDelta, ID: "t1", Delta: "hello "},
		{Type: fantasy.StreamPartTypeTextDelta, ID: "t1", Delta: "world"},
		{Type: fantasy.StreamPartTypeTextEnd, ID: "t1"},
		{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop,
			Usage: fantasy.Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}},
	}}
	r := NewRunner(fm, nil, RunnerConfig{})

	var sb strings.Builder
	steps := 0
	ans, err := r.Stream(context.Background(), "hi", nil, StreamCallbacks{
		OnText: func(d string) { sb.WriteString(d) },
		OnStep: func(Usage) { steps++ },
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	// Deltas arrived incrementally via the callback...
	if sb.String() != "hello world" {
		t.Errorf("streamed text = %q, want %q", sb.String(), "hello world")
	}
	// ...and the assembled Answer matches, with usage and one step.
	if ans.Content != "hello world" {
		t.Errorf("Content = %q", ans.Content)
	}
	if ans.Usage.TotalTokens != 5 {
		t.Errorf("Usage = %+v, want total=5", ans.Usage)
	}
	if steps != 1 {
		t.Errorf("OnStep calls = %d, want 1", steps)
	}
}

func TestRunner_CompactHistory(t *testing.T) {
	// Under budget → unchanged, no model call needed.
	r := NewRunner(&fakeModel{}, nil, RunnerConfig{})
	short := []fantasy.Message{fantasy.NewUserMessage("hi"), fantasy.NewUserMessage("there")}
	if got := r.CompactHistory(context.Background(), short); len(got) != 2 {
		t.Errorf("short history should be unchanged, got %d", len(got))
	}

	// Over budget → oldest turns summarized into a leading system message,
	// compactKeepRecent turns kept verbatim.
	fm := &fakeModel{responses: []*fantasy.Response{{
		Content:      fantasy.ResponseContent{fantasy.TextContent{Text: "SUMMARY"}},
		FinishReason: fantasy.FinishReasonStop,
	}}}
	r2 := NewRunner(fm, nil, RunnerConfig{})
	big := strings.Repeat("x", 5000) // 12 × 5000 = 60000 > compactMaxChars
	var hist []fantasy.Message
	for i := 0; i < 12; i++ {
		hist = append(hist, fantasy.NewUserMessage(big))
	}
	got := r2.CompactHistory(context.Background(), hist)
	if len(got) != 1+compactKeepRecent {
		t.Fatalf("compacted len = %d, want %d", len(got), 1+compactKeepRecent)
	}
	wantRole := fantasy.NewSystemMessage("x").Role
	if got[0].Role != wantRole || !strings.Contains(messageText(got[0]), "SUMMARY") {
		t.Errorf("first message should be the system summary, got role=%s text=%q", got[0].Role, messageText(got[0]))
	}

	// Summarization failure → history returned unchanged (best-effort).
	rErr := NewRunner(&errModel{}, nil, RunnerConfig{})
	if got := rErr.CompactHistory(context.Background(), hist); len(got) != len(hist) {
		t.Errorf("on summarize error, history must be unchanged, got %d want %d", len(got), len(hist))
	}
}

// errModel fails Generate, exercising the best-effort compaction fallback.
type errModel struct{ fakeModel }

func (errModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return nil, errors.New("model down")
}

func TestNewRunner_defaultMaxSteps(t *testing.T) {
	r := NewRunner(&fakeModel{}, nil, RunnerConfig{})
	if r.cfg.MaxSteps != defaultMaxSteps {
		t.Errorf("MaxSteps = %d, want default %d", r.cfg.MaxSteps, defaultMaxSteps)
	}
	r2 := NewRunner(&fakeModel{}, nil, RunnerConfig{MaxSteps: 5})
	if r2.cfg.MaxSteps != 5 {
		t.Errorf("MaxSteps = %d, want 5", r2.cfg.MaxSteps)
	}
}
