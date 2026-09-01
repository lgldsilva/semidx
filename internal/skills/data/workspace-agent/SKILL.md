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

## Combined workflows

The workspace agent is most useful when you mix **repository state** with
**code content**:

### Orienting in a new branch

1. `repo_branches` + `repo_status` — see what changed and what is dirty.
2. `semantic_status` — confirm the current branch is indexed.
3. `semantic_search` — find the code relevant to the branch's purpose.
4. `semantic_diff` (or `semidx diff main..HEAD`) — see which symbols changed.

### Planning a refactor

1. `semantic_search` — locate the symbol by behavior.
2. `semantic_explain` / `semantic_callers` — understand its contract and callers.
3. `semantic_impact` — see transitive blast radius.
4. `semantic_path` — trace how the affected files connect across packages.
5. Make edits, then `semantic_diff` to confirm the symbol delta.

### Answering "how does X flow through the system?"

1. `semantic_search` — find the entry point and the sink.
2. `semantic_path from=<entry> to=<sink>` — walk the shortest dependency path.
3. `semantic_subgraph` around intermediate files — explore alternatives and
   hubs.

Use repo tools for "what is the state?" and semidx tools for "what does the
 code mean and how is it connected?"
