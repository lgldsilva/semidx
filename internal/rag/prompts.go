package rag

import (
	"fmt"
	"strings"

	"github.com/lgldsilva/semidx/internal/chat"
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

// assemblePrompt builds the full chat messages for the LLM.
// It returns: [system_msg (instructions + context combined), history..., user_question].
// Instructions and context are combined into a single system message to avoid
// confusing some LLM providers (e.g. Gemini) that handle multiple system messages poorly.
func assemblePrompt(question string, contextStr string, history []chat.Message) []chat.Message {
	messages := make([]chat.Message, 0, 2+len(history))

	// Single system message combining instructions and context.
	systemContent := systemPrompt
	if contextStr != "" {
		systemContent += "\n\nHere is the relevant context from the indexed files:\n\n" + contextStr
	}
	messages = append(messages, chat.Message{Role: "system", Content: systemContent})

	// Conversation history.
	messages = append(messages, history...)

	// Current user question.
	messages = append(messages, chat.Message{Role: "user", Content: question})

	return messages
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
