---
name: workspace-agent
description: Conversational workspace agent via semidx — combine semantic search, git tools, and MCP to answer questions about code AND repository state (worktrees, branches, index status). Use when the task needs facts about both file content and repository metadata.
---

# semidx workspace agent

semidx is now a **workspace agent**: it can answer questions about both the
**content** of your projects (via semantic search) and their **state** (git
worktrees, branches, index status).

## MCP tools

- `semantic_search` — search by meaning
- `semantic_search_multi` — search multiple projects, fused results
- `semantic_projects` — list indexed projects
- `semantic_status` — check if a project is indexed
- `semantic_ask` — ask a question (RAG or agentic, gated on LLM)
- `repo_worktrees` — list git worktrees (local MCP only)
- `repo_branches` — list branches (local MCP only)
- `repo_status` — repo working tree state (local MCP only)

## Example prompts

- "Where is the auth validation implemented?"
- "How many worktrees does the repo have?"
- "Check if the project is indexed"
- "List all indexed projects"
- "What branch am I on and is the working tree clean?"
