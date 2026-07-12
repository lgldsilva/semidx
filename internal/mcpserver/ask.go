package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// AskBackend extends Backend with RAG-augmented chat.
type AskBackend interface {
	Backend
	Ask(ctx context.Context, project, question string, topK int) (*AskOutput, error)
}

// AskOutput is a backend-neutral RAG answer.
type AskOutput struct {
	Answer   string
	Sources  []AskSource
	Model    string
	Fallback bool // true when embedding was unavailable (keyword-only search)
}

// AskSource is one source chunk used in the answer.
type AskSource struct {
	Path      string
	StartLine int
	EndLine   int
	Score     float64
	Keyword   bool
}

type askInput struct {
	Project  string `json:"project" jsonschema:"the registered project to ask about"`
	Question string `json:"question" jsonschema:"the natural-language question"`
	TopK     int    `json:"top_k,omitempty" jsonschema:"number of chunks to retrieve (default 5)"`
}

func askHandler(b AskBackend) mcp.ToolHandlerFor[askInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in askInput) (*mcp.CallToolResult, any, error) {
		topK := in.TopK
		if topK == 0 {
			topK = 5
		}
		out, err := b.Ask(ctx, in.Project, in.Question, topK)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(formatAsk(out)), nil, nil
	}
}

func formatAsk(out *AskOutput) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(out.Answer))
	b.WriteString("\n\n---\n")
	if out.Fallback {
		b.WriteString("*[keyword fallback]*\n\n")
	}
	fmt.Fprintf(&b, "Model: %s\n", out.Model)
	if len(out.Sources) > 0 {
		b.WriteString("\nSources:\n")
		for i, s := range out.Sources {
			kind := ""
			if s.Keyword {
				kind = " [keyword]"
			}
			fmt.Fprintf(&b, "%d. %s:%d-%d (%.3f)%s\n",
				i+1, s.Path, s.StartLine, s.EndLine, s.Score, kind)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
