package rag

import (
	"fmt"
	"strings"
)

const systemPrompt = `You are a helpful AI assistant that answers questions about a codebase or document collection. 
You are given relevant chunks from the indexed files as context.
Answer the user's question based on the provided context.
If the context doesn't contain enough information, say so — don't make things up.
When citing information, reference the file path and line numbers from the context.
Be concise and precise.`

// AgentPrompt is the system prompt fragment for the workspace agent.
// It instructs the model to propose actions for confirmation rather than
// executing them directly. Included when the agent has tool-calling capability.
const AgentPrompt = `You are a workspace agent with tools for searching code, checking git state, and proposing index/reindex actions. When you have enough information to answer, respond directly. Cite whether facts came from git (live) or the semidx index. For actions: propose what you would do and ask for confirmation before proceeding.`

// systemContent combines the RAG instructions and the assembled context into a
// single system prompt. One message, not two: some LLM providers (e.g. Gemini)
// handle multiple system messages poorly.
func systemContent(contextStr string) string {
	if contextStr == "" {
		return systemPrompt
	}
	return systemPrompt + "\n\nHere is the relevant context from the indexed files:\n\n" + contextStr
}

// formatSourceBlock formats a single source as a block for inclusion in
// the prompt context string.
func formatSourceBlock(s Source) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- File: %s (lines %d-%d) ---\n", s.File, s.StartLine, s.EndLine)
	b.WriteString(s.Content)
	if !strings.HasSuffix(s.Content, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("---\n\n")
	return b.String()
}
