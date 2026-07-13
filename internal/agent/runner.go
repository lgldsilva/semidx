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

// ToolCallRecord traces one tool invocation for provenance. Result carries the
// tool's textual output (JSON for the read tools); Error is set when the tool
// reported a failure. Both let callers reconstruct real sources and audit the
// loop after the fact.
type ToolCallRecord struct {
	Tool   string
	Args   string
	Result string
	Error  string
}

// Usage reports token accounting for one agent turn, aggregated across every
// step of the loop. It mirrors fantasy.Usage so callers get cache/reasoning
// breakdowns without importing fantasy.
type Usage struct {
	InputTokens         int64
	OutputTokens        int64
	TotalTokens         int64
	ReasoningTokens     int64
	CacheCreationTokens int64
	CacheReadTokens     int64
}

// Answer is the final agent response with provenance. Messages carries the
// assistant (with tool_calls) and tool-result messages produced this turn so a
// caller can persist them and give the model real multi-turn tool memory next
// turn. Usage aggregates token accounting across the loop's steps.
type Answer struct {
	Content  string
	Trace    []ToolCallRecord
	Model    string
	Usage    Usage
	Messages []fantasy.Message
}

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
	ag := fantasy.NewAgent(r.model, r.agentOptions()...)
	res, err := ag.Generate(ctx, fantasy.AgentCall{Prompt: question, Messages: history})
	if err != nil {
		return nil, fmt.Errorf("agent: %w", err)
	}
	return &Answer{
		Content:  res.Response.Content.Text(),
		Trace:    traceFromSteps(res.Steps),
		Model:    r.model.Model(),
		Usage:    usageFrom(res.TotalUsage),
		Messages: messagesFromSteps(res.Steps),
	}, nil
}

// agentOptions builds the fantasy agent options shared by every run.
func (r *Runner) agentOptions() []fantasy.AgentOption {
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
	return opts
}

// traceFromSteps flattens the tool calls across all steps into the record
// format the rest of semidx already consumes (cmd/chatrag, mcpserver). Each
// call is joined to its result (matched by tool-call id within the step) so the
// trace carries the tool's actual output, not just the arguments.
func traceFromSteps(steps []fantasy.StepResult) []ToolCallRecord {
	var trace []ToolCallRecord
	for _, step := range steps {
		results := make(map[string]fantasy.ToolResultContent)
		for _, tr := range step.Content.ToolResults() {
			results[tr.ToolCallID] = tr
		}
		for _, tc := range step.Content.ToolCalls() {
			rec := ToolCallRecord{Tool: tc.ToolName, Args: tc.Input}
			if tr, ok := results[tc.ToolCallID]; ok {
				rec.Result, rec.Error = toolResultText(tr)
			}
			trace = append(trace, rec)
		}
	}
	return trace
}

// toolResultText extracts the textual output and error message from a tool
// result. The read tools always return text (JSON); an error result carries the
// failure message instead.
func toolResultText(tr fantasy.ToolResultContent) (result, errMsg string) {
	switch v := tr.Result.(type) {
	case fantasy.ToolResultOutputContentText:
		return v.Text, ""
	case fantasy.ToolResultOutputContentMedia:
		return v.Text, ""
	case fantasy.ToolResultOutputContentError:
		if v.Error != nil {
			return "", v.Error.Error()
		}
		return "", "tool error"
	}
	return "", ""
}

// messagesFromSteps flattens every step's messages (assistant with tool_calls,
// tool results, and the final assistant text) in order. Prepend the user
// message and this is the full record of the turn for multi-turn history.
func messagesFromSteps(steps []fantasy.StepResult) []fantasy.Message {
	var msgs []fantasy.Message
	for _, step := range steps {
		msgs = append(msgs, step.Messages...)
	}
	return msgs
}

// usageFrom converts a fantasy usage struct to the agent-package equivalent.
func usageFrom(u fantasy.Usage) Usage {
	return Usage{
		InputTokens:         u.InputTokens,
		OutputTokens:        u.OutputTokens,
		TotalTokens:         u.TotalTokens,
		ReasoningTokens:     u.ReasoningTokens,
		CacheCreationTokens: u.CacheCreationTokens,
		CacheReadTokens:     u.CacheReadTokens,
	}
}
