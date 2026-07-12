package agent

import (
	"context"
	"fmt"

	"github.com/lgldsilva/semidx/internal/chat"
)

const defaultMaxRounds = 6

// Tool is one capability the agent can invoke.
type Tool interface {
	Def() chat.ToolDef
	Run(ctx context.Context, args string) (string, error) // args JSON -> result text
}

// ToolCallRecord traces one tool invocation for provenance.
type ToolCallRecord struct {
	Tool   string
	Args   string
	Result string
	Error  string
}

// Agent runs the LLM<->tools loop.
type Agent struct {
	chat      chat.Client
	tools     map[string]Tool
	resolver  ScopeResolver
	maxRounds int
}

// NewAgent creates an agent. resolver is optional (nil disables scope resolution).
func NewAgent(client chat.Client, tools []Tool, resolver ScopeResolver) *Agent {
	tm := make(map[string]Tool, len(tools))
	for _, t := range tools {
		tm[t.Def().Name] = t
	}
	return &Agent{
		chat:      client,
		tools:     tm,
		resolver:  resolver,
		maxRounds: defaultMaxRounds,
	}
}

// Answer is the final agent response with provenance.
type Answer struct {
	Content string
	Trace   []ToolCallRecord
	Model   string
}

// Ask runs the tool-calling loop and returns the final answer.
func (a *Agent) Ask(ctx context.Context, question string, history []chat.Message) (*Answer, error) {
	defs := make([]chat.ToolDef, 0, len(a.tools))
	for _, t := range a.tools {
		defs = append(defs, t.Def())
	}

	messages := make([]chat.Message, 0, 2+len(history))
	messages = append(messages, chat.Message{Role: "system", Content: SystemPrompt})
	messages = append(messages, history...)
	messages = append(messages, chat.Message{Role: "user", Content: question})
	var trace []ToolCallRecord

	for round := 0; round < a.maxRounds; round++ {
		resp, err := a.chat.SendMessage(ctx, chat.Request{
			Messages: messages,
			Tools:    defs,
		})
		if err != nil {
			return nil, fmt.Errorf("chat round %d: %w", round, err)
		}

		if len(resp.ToolCalls) == 0 {
			// Final answer — no tools needed.
			return &Answer{
				Content: resp.Content,
				Trace:   trace,
				Model:   resp.Model,
			}, nil
		}

		// Execute each tool call.
		for _, tc := range resp.ToolCalls {
			tool, ok := a.tools[tc.Name]
			if !ok {
				msg := fmt.Sprintf("unknown tool: %s", tc.Name)
				messages = append(messages, chat.Message{Role: "assistant", Content: msg})
				trace = append(trace, ToolCallRecord{Tool: tc.Name, Args: tc.Args, Error: msg})
				continue
			}

			result, err := tool.Run(ctx, tc.Args)
			errMsg := ""
			if err != nil {
				errMsg = err.Error()
				result = `{"error":"` + errMsg + `"}`
			}
			messages = append(messages, chat.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("Tool %s result:\n%s", tc.Name, result),
			})
			trace = append(trace, ToolCallRecord{
				Tool: tc.Name, Args: tc.Args,
				Result: result, Error: errMsg,
			})
		}
	}

	// Max rounds reached without final answer.
	return &Answer{
		Content: "Maximum iterations reached without a final answer. Try rephrasing.",
		Trace:   trace,
	}, nil
}
