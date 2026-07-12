package agent

import (
	"context"
	"errors"
	"testing"

	"charm.land/fantasy"
)

// fakeModel is a minimal fantasy.LanguageModel that replays pre-programmed
// Generate responses. Only Generate is exercised by Runner; the object/stream
// methods are stubs.
type fakeModel struct {
	responses []*fantasy.Response
	idx       int
	gotTools  []fantasy.Tool
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

func (f *fakeModel) Stream(context.Context, fantasy.Call) (fantasy.StreamResponse, error) {
	return nil, errors.New("stream not implemented in fake")
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
