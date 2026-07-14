package rag

import (
	"strings"

	"github.com/lgldsilva/semidx/internal/privacy"
)

// filterSensitiveSources drops chunks whose path matches the privacy deny-list
// before they enter the LLM context (same rule the legacy Pipeline applied).
func filterSensitiveSources(results []SearchResult) []Source {
	sources := make([]Source, 0, len(results))
	for _, r := range results {
		if privacy.IsSensitive(r.FilePath) {
			continue
		}
		sources = append(sources, Source{
			File:      r.FilePath,
			StartLine: r.StartLine,
			EndLine:   r.EndLine,
			Content:   r.Content,
			Score:     r.Score,
		})
	}
	return sources
}

// diversify caps the number of chunks from each file and each project to
// ensure a balanced context. Works on Source slice (pre-assembleContext).
// maxPerFile <= 0 means no per-file cap; maxPerProject <= 0 means no per-project
// cap. The sources slice is modified in place and returned.
func diversify(sources []Source, maxPerFile, maxPerProject int) []Source {
	if maxPerFile <= 0 && maxPerProject <= 0 {
		return sources
	}
	if maxPerFile <= 0 {
		maxPerFile = len(sources) + 1 // effectively unlimited
	}
	if maxPerProject <= 0 {
		maxPerProject = len(sources) + 1
	}

	fileCount := make(map[string]int, len(sources)/2)
	projectCount := make(map[string]int, len(sources)/2)
	out := make([]Source, 0, len(sources))

	for _, s := range sources {
		proj := extractProject(s.File)
		if projectCount[proj] >= maxPerProject {
			continue
		}
		if fileCount[s.File] >= maxPerFile {
			continue
		}
		projectCount[proj]++
		fileCount[s.File]++
		out = append(out, s)
	}
	return out
}

// extractProject extracts a project label from a file path.
// Simple heuristic: everything before the first '/' or ':',
// skipping a leading slash.
func extractProject(fp string) string {
	for i := 0; i < len(fp); i++ {
		if fp[i] == '/' || fp[i] == ':' {
			if i == 0 {
				continue // leading /absolute/path → skip to next separator
			}
			return fp[:i]
		}
	}
	return "default"
}

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
