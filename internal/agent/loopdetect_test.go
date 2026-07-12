package agent

import (
	"testing"

	"charm.land/fantasy"
)

// stepWithToolCall builds a StepResult whose response contains a single tool
// call with the given name and input.
func stepWithToolCall(name, input string) fantasy.StepResult {
	return fantasy.StepResult{
		Response: fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.ToolCallContent{ToolName: name, Input: input},
			},
		},
	}
}

func TestLoopDetection_repeatedCallStops(t *testing.T) {
	det := LoopDetection(loopWindow, 3)

	var steps []fantasy.StepResult
	// Two identical calls: not enough to trip a threshold of 3.
	steps = append(steps, stepWithToolCall("search", `{"q":"x"}`))
	steps = append(steps, stepWithToolCall("search", `{"q":"x"}`))
	if det(steps) {
		t.Fatal("should not stop after 2 identical calls (threshold 3)")
	}
	// Third identical call trips it.
	steps = append(steps, stepWithToolCall("search", `{"q":"x"}`))
	if !det(steps) {
		t.Fatal("should stop after 3 identical tool calls")
	}
}

func TestLoopDetection_distinctCallsDoNotStop(t *testing.T) {
	det := LoopDetection(loopWindow, 3)
	steps := []fantasy.StepResult{
		stepWithToolCall("search", `{"q":"a"}`),
		stepWithToolCall("search", `{"q":"b"}`), // different input
		stepWithToolCall("status", `{"q":"a"}`), // different tool
		stepWithToolCall("search", `{"q":"a"}`), // only 2nd "search/a"
	}
	if det(steps) {
		t.Fatal("distinct tool calls must not trip loop detection")
	}
}

func TestLoopDetection_windowExcludesOldRepeats(t *testing.T) {
	det := LoopDetection(2, 3) // only the last 2 steps count
	steps := []fantasy.StepResult{
		stepWithToolCall("search", `{"q":"x"}`),
		stepWithToolCall("search", `{"q":"x"}`),
		stepWithToolCall("search", `{"q":"x"}`),
	}
	// Window of 2 sees only 2 identical calls → below threshold 3.
	if det(steps) {
		t.Fatal("window should exclude the oldest repeat")
	}
}

func TestLoopDetection_disabled(t *testing.T) {
	steps := []fantasy.StepResult{
		stepWithToolCall("search", `{"q":"x"}`),
		stepWithToolCall("search", `{"q":"x"}`),
		stepWithToolCall("search", `{"q":"x"}`),
	}
	if LoopDetection(0, 3)(steps) || LoopDetection(10, 0)(steps) {
		t.Fatal("non-positive window/maxRepeats must disable detection")
	}
}
