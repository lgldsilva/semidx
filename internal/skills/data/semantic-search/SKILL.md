---
name: semantic-search
description: >
  Use for intent/behavior code search ("where do we validate tokens?", "how is
  retry handled?") via semidx MCP semantic_search or CLI sgrep/search — not for
  exact-string grep. Prefer this over ripgrep when the identifier is unknown.
  If unsure the project is indexed, follow the auto-index skill first. Covers
  scores, stale hits, and keyword fallback.
---

# Semantic code search with semidx

`semidx` indexes code into a vector database and searches it semantically: it
matches on *meaning*, not literal text. A query like "where is the retry backoff
implemented" finds the code even when it never uses the words "retry" or
"backoff". Use it to explore unfamiliar code, find behavior across files, or
locate a concept whose exact name you don't know.

## Semantic search vs grep — pick the right tool

Reach for **semantic search (semidx)** when:
- The query is about intent or behavior ("how are sessions expired?", "input
  validation for uploads").
- You don't know the exact identifier, or the concept spans several names.
- You want the most *relevant* few results, ranked, not every literal hit.

Reach for **grep / ripgrep** when:
- You know the exact string, symbol, or path (`grep -rn "func Verify"`).
- You need *every* occurrence (a rename, an audit), not the top matches.
- The repository isn't indexed by a semidx server.

They complement each other: use semantic search to find the right region, then
grep for the exact call sites within it.

## Prerequisite: point at a server

semidx is client-server. Once, per machine, log in to the server that holds the
index (ask the server admin for an API token):

```bash
semidx login https://semidx.example.com --token <api-token>
```

This saves the server URL and token to `~/.config/semidx/config.yaml`. All the
commands below then talk to that server.

## CLI commands

`sgrep` gives classic `file:line:content` output — ideal for piping and for
jumping straight to a location:

```bash
semidx sgrep --project myapp --query "verify auth token"
```

`search` gives ranked results with a content preview and a relevance score:

```bash
semidx search --project myapp --query "how are database migrations applied" --top-k 5
```

Add `--json` to either for machine-readable output:

```bash
semidx search --project myapp --query "rate limiting" --json
```

List what the server can search, and register/queue a git repo the server will
clone and index itself:

```bash
semidx repo add https://github.com/acme/myapp.git --name myapp
```

Every command supports `--help` (e.g. `semidx search --help`).

## MCP tools (for AI agents)

When semidx is wired in as an MCP server, four tools are available:

- `semantic_search` — args `project`, `query`, optional `model`, `top_k`, `graph`, `graph_depth`.
  Returns ranked `file:line` matches with previews.
- `semantic_status` — arg `project`. Reports whether the project is indexed, how many
  files are indexed, and its status (ready, indexing, etc). Use this before searching to
  decide whether to index first.
- `semantic_projects` — lists registered projects and their indexing status.
- `semantic_reindex` — args `project`, optional `type` (`full` | `git_history`).
  Queues a re-index of an already-registered project.

**Structural code-intelligence tools**: beyond search, five MCP tools answer
*relationship* questions grep/search can't: `semantic_callers`, `semantic_explain`,
`semantic_impact` (blast radius), `semantic_deadcode`, and `semantic_diff`. Use
them for "who calls this?", "what breaks if I change it?", "is this unused?",
and "what symbols changed between refs?". See the **code-intel** and
**impact-before-refactor** skills, and reach for `semantic_impact` *before* any
refactor of a symbol you didn't write.

**Dependency-graph tools**: `semantic_subgraph` (neighborhood around a file) and
`semantic_path` (how file A reaches file B) walk the indexed file↔package import
graph. Use them for "how do these two files connect?" — see the
**semantic-graph** skill for the node/edge model and the walk budgets.

**New since v0.x**: `semidx status` CLI command and `semantic_status` MCP tool. Use
`semidx status --project .` to check indexing health before searching. Use
`semidx index --branch <name>` to index another branch as a separate project
(e.g., `myrepo@develop`).

Tools accept **project names only**, never filesystem paths — an agent cannot
index an arbitrary directory. Business problems (unknown project, server error)
come back as tool results flagged `isError`, with a readable message; read it and
adjust rather than retrying blindly.

## Reading the results

- **Score**: higher is more relevant within the returned set. It may be cosine,
  RRF, lexical, or reranked score; it is not a calibrated probability. Treat
  the top results as candidates, not certainties — skim the previews before
  acting.
- **Keyword fallback**: if the response is flagged as fallback (a `[warning]`
  line in text output, or `"fallback": true` in JSON), the embedding backend was
  unavailable and the server used lexically ranked keyword search instead.
  Results are literal matches ordered by lexical relevance, not semantic ones —
  re-run once embeddings are back if semantic recall matters.
- **A stale index** returns old code. If results look outdated after a big change,
  re-index (`semidx repo add`/reindex, or the `semantic_reindex` MCP tool) and
  search again.

## Combined workflow: from concept to structure

Semantic search finds *where* a concept lives; the structural tools explain
*how it is wired*. Use them as a sequence:

1. **Locate the concept** with `semantic_search`:
   ```
   semantic_search project=myapp query="how is retry handled"
   ```
   → yields `internal/retry/retry.go:45` (function `WithBackoff`).

2. **Understand the symbol** with `semantic_explain`:
   ```
   semantic_explain file=internal/retry/retry.go line=45 project=myapp
   ```
   → kind, direct dependencies, importers, related tests.

3. **See who calls it** with `semantic_callers`:
   ```
   semantic_callers file=internal/retry/retry.go line=45 project=myapp
   ```
   → files that import the package (direct callers).

4. **Trace the dependency path** between two files with `semantic_path`:
   ```
   semantic_path project=myapp from=cmd/myapp/main.go to=internal/retry/retry.go
   ```
   → shortest import walk (main.go → internal/retry/ → retry.go).

5. **Before refactoring**, run `semantic_impact` to get the transitive blast
   radius (see the `impact-before-refactor` skill).

Use this chain whenever a search result lands you on a symbol you need to
change, delete, or explain to someone else.

## Anti-patterns

- Don't use semantic search for exact-string work (a symbol rename, counting
  occurrences) — grep is faster and complete there.
- Don't trust the top hit blindly; the score ranks relevance, it doesn't prove
  correctness. Open the file.
- Don't paste secrets into a query — queries are sent to the server and embedded.
- Don't assume the index is current; re-index after large changes.
