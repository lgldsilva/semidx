package agent

import (
	"context"
	"fmt"

	"charm.land/fantasy"
)

// defaultMaxSteps caps the agent loop when the caller does not configure one.
// fantasy imposes no default cap, so this is our guard-rail (Crush relies on
// summarization + loop-detection; we keep an explicit ceiling too).
const defaultMaxSteps = 20

// RunnerConfig configures a fantasy-backed agent runner.
type RunnerConfig struct {
	SystemPrompt string
	MaxSteps     int      // <= 0 uses defaultMaxSteps
	Temperature  *float64 // nil = provider default
}

// Runner drives a fantasy multi-step tool-calling loop over a language model
// and a fixed set of tools. It replaces the hand-rolled Agent.Ask loop: the
// step cap and loop-detection are fantasy StopConditions, retry/backoff and
// JSON repair come from fantasy, and the configured model/temperature are
// actually honored (the old loop hard-coded the model and ignored temperature).
type Runner struct {
	model fantasy.LanguageModel
	tools []fantasy.AgentTool
	cfg   RunnerConfig
}

// NewRunner builds a Runner. An empty tools slice yields a plain chat loop.
func NewRunner(model fantasy.LanguageModel, tools []fantasy.AgentTool, cfg RunnerConfig) *Runner {
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = defaultMaxSteps
	}
	return &Runner{model: model, tools: tools, cfg: cfg}
}

// Ask runs the loop for one user turn and returns the final text plus a trace
// of the tool calls made along the way. history carries prior turns (including
// assistant tool_calls and tool results) so multi-turn context is preserved.
func (r *Runner) Ask(ctx context.Context, question string, history []fantasy.Message) (*Answer, error) {
	opts := []fantasy.AgentOption{
		fantasy.WithStopConditions(
			fantasy.StepCountIs(r.cfg.MaxSteps),
			LoopDetection(loopWindow, loopMaxRepeats),
		),
	}
	if len(r.tools) > 0 {
		opts = append(opts, fantasy.WithTools(r.tools...))
	}
	if r.cfg.SystemPrompt != "" {
		opts = append(opts, fantasy.WithSystemPrompt(r.cfg.SystemPrompt))
	}
	if r.cfg.Temperature != nil {
		opts = append(opts, fantasy.WithTemperature(*r.cfg.Temperature))
	}

	ag := fantasy.NewAgent(r.model, opts...)
	res, err := ag.Generate(ctx, fantasy.AgentCall{Prompt: question, Messages: history})
	if err != nil {
		return nil, fmt.Errorf("agent: %w", err)
	}
	return &Answer{
		Content: res.Response.Content.Text(),
		Trace:   traceFromSteps(res.Steps),
		Model:   r.model.Model(),
	}, nil
}

// traceFromSteps flattens the tool calls across all steps into the record
// format the rest of semidx already consumes (cmd/chatrag, mcpserver).
func traceFromSteps(steps []fantasy.StepResult) []ToolCallRecord {
	var trace []ToolCallRecord
	for _, step := range steps {
		for _, tc := range step.Content.ToolCalls() {
			trace = append(trace, ToolCallRecord{Tool: tc.ToolName, Args: tc.Input})
		}
	}
	return trace
}
