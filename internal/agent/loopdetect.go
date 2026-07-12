package agent

import (
	"crypto/sha256"
	"encoding/hex"

	"charm.land/fantasy"
)

// Loop-detection defaults, mirroring Crush's detector: look at the last
// loopWindow steps and stop if one identical tool call recurs loopMaxRepeats
// times.
const (
	loopWindow     = 10
	loopMaxRepeats = 5
)

// LoopDetection returns a fantasy StopCondition that halts the agent when the
// same tool call (tool name + input arguments) recurs too often within a
// sliding window of recent steps. It is a cheap guard against a model that gets
// stuck calling the same tool with the same arguments forever — the fixed step
// cap alone would still burn several rounds of tokens first.
//
// window is how many trailing steps to consider; maxRepeats is how many
// identical tool-call signatures within that window trigger a stop. Non-positive
// values disable the check.
func LoopDetection(window, maxRepeats int) fantasy.StopCondition {
	return func(steps []fantasy.StepResult) bool {
		if window <= 0 || maxRepeats <= 0 {
			return false
		}
		start := len(steps) - window
		if start < 0 {
			start = 0
		}
		counts := make(map[string]int)
		for _, step := range steps[start:] {
			for _, sig := range toolCallSignatures(step) {
				counts[sig]++
				if counts[sig] >= maxRepeats {
					return true
				}
			}
		}
		return false
	}
}

// toolCallSignatures returns a stable hash for each tool call in a step, over
// the tool name and its input arguments.
func toolCallSignatures(step fantasy.StepResult) []string {
	var sigs []string
	for _, c := range step.Content {
		if c.GetType() != fantasy.ContentTypeToolCall {
			continue
		}
		tc, ok := fantasy.AsContentType[fantasy.ToolCallContent](c)
		if !ok {
			continue
		}
		sum := sha256.Sum256([]byte(tc.ToolName + "\x00" + tc.Input))
		sigs = append(sigs, hex.EncodeToString(sum[:]))
	}
	return sigs
}
