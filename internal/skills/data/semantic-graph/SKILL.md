---
name: semantic-graph
description: Use when you need structural dependency links — neighbors of a file, how file A reaches file B, or hubs in the import graph. Covers semidx CLI graph, MCP semantic_subgraph/semantic_path, and the bipartite file↔package model (not file→file).
---

# Dependency graph with semidx

`semidx` indexes import edges as **file → package-dir** (e.g.
`cmd/semidx/main.go → internal/store/`), then walks them with synthetic
**package-dir → file** hops so two files in related packages can connect.
Use this skill for structural questions; use `semantic-search` for intent /
behavior queries.

## When to use the graph (vs search / grep)

Reach for the **graph** when:
- "What does this file import / connect to?"
- "How does A communicate with B?" (shortest dependency path)
- You need blast-radius *structure* after finding a symbol (pair with
  `semantic_callers` / `semantic_impact` when you have a file:line)

Prefer **semantic search** for "where is retry handled?" Prefer **grep** for
exact identifiers.

## Mental model (important)

Edges are **not** file→file. A typical path looks like:

```
main.go → internal/store/ → store.go
```

Package nodes end with `/`. In JSON, nodes have `kind: "file" | "package"`.
Do not treat a package id as an openable source file.

Paths may be **directed** (follow import direction) or **undirected** (allow
reverse hops). When undirected, responses set `directed: false` and edges may
have `reverse: true` — that means "B imports A", not "A imports B".

Large neighborhoods may set `truncated: true` (DoS budgets: depth / visit /
edges). Narrow the seed or depth rather than retrying blindly.

## CLI

```bash
semidx graph stats --project .
semidx graph neighbors --project . --file internal/store/store.go
semidx graph path --from cmd/semidx/main.go --to internal/store/store.go --json
semidx graph path --from a.go --to b.go --undirected --json
```

`stats` needs a local index (`--local` / local backend). `neighbors` and
`path` work against a remote server API as well.

## MCP tools

- `semantic_subgraph` — args `project`, optional `file`, `depth`, `limit`.
  Ego neighborhood around a file as nodes + edges (imports + package
  membership). Omit `file` for a hub sample of the whole project.
- `semantic_path` — args `project`, `from`, `to`, optional `max_depth`,
  `undirected`. Shortest path; check `found`, `directed`, and `hops`.

Don't confuse `semantic_subgraph` with `semantic_neighbors`: the latter is the
raw adjacency map (`imports` / `exports` string lists) for one file, with no
package hops and no walk budget.

Related (already in the MCP server, not graph BFS): `semantic_callers`,
`semantic_impact`, `semantic_explain` for symbol-at-file:line analysis, and
`semantic_trace` for BFS depth-per-file from seed files.

## Anti-patterns

- Don't assume an undirected path proves A imports B — read `directed` /
  `reverse`.
- Don't open package-dir nodes as files.
- Don't use the graph for exact string lookup — grep wins there.
- Don't ignore `truncated: true`; results are incomplete by design.
