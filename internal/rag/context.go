package rag

import "strings"

// assembleContext formats sources into a context string, respecting a token
// budget. Sources are assumed pre-sorted by relevance (highest score first).
// Token estimation uses a rough heuristic: ~4 chars per token.
// Chunks that don't fully fit are truncated at rune boundaries (never mid-UTF-8
// character) and always include the closing "---" marker. Lowest-score chunks
// are dropped entirely when budget runs out.
func assembleContext(sources []Source, budgetTokens int) string {
	if len(sources) == 0 || budgetTokens <= 0 {
		return ""
	}

	budgetChars := budgetTokens * 4
	var b strings.Builder
	b.Grow(budgetChars)

	for _, s := range sources {
		block := formatSourceBlock(s)
		if b.Len()+len(block) <= budgetChars {
			// Full block fits.
			b.WriteString(block)
			continue
		}

		// Partial fit: truncate content at rune boundary, keep the closing marker.
		if b.Len() >= budgetChars {
			break
		}
		remaining := budgetChars - b.Len()
		if remaining < 60 { // minimum meaningful snippet threshold
			break
		}

		// Find the largest rune count whose full block fits.
		runes := []rune(s.Content)
		for n := len(runes); n > 0; n-- {
			candidate := s
			candidate.Content = string(runes[:n])
			cb := formatSourceBlock(candidate)
			if b.Len()+len(cb) <= budgetChars {
				b.WriteString(cb)
				break
			}
		}
		break // after truncation don't add more sources
	}

	return b.String()
}
