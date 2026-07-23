---
name: code-intel
description: Use the semidx structural code-intelligence tools when you need dependency relationships rather than text — who imports/calls a symbol, what breaks if you change it (blast radius), what a symbol depends on and who tests it, which symbols are unused (dead code), and what code symbols changed between two git refs. Triggers on "who calls X", "what depends on", "is this used/dead", "impact of changing", "what changed in this branch", and any refactor where unseen callers could break. These tools use the indexed dependency graph + tree-sitter symbols, so they catch cross-file relationships grep cannot.
---

# Code intelligence with semidx (structure, not text)

`semantic_search` finds code by *meaning*. The **code-intelligence** tools find
code by *relationship*: who depends on this, what breaks if I change it, what's
unused, what changed. They read the indexed dependency graph (`file_dependencies`)
and tree-sitter symbols — relationships that no text search (grep or semantic)
can surface.

Use them **before** edits that could ripple, and **while** orienting in a
codebase you didn't write. They are the difference between a blind refactor and
a safe one.

## The five tools

All five are available as both MCP tools and CLI commands. MCP args use
`file`/`line`; CLI uses `file:line`. `project` is optional everywhere — it
defaults to the project enclosing your current directory.

| Tool | Answers | MCP args | CLI |
|---|---|---|---|
| **callers** | Who imports/depends on the package containing this symbol? | `file`, `line`, `project?` | `semidx callers <file:line>` |
| **explain** | What is this symbol? Its kind, deps, importers, related tests. | `file`, `line`, `project?` | `semidx explain <file:line>` |
| **impact** | What's the blast radius if I change this? Transitive reverse deps. | `file`, `line`, `project?`, `depth?` (default 5, max 10) | `semidx explain`/graph |
| **deadcode** | Which symbols are unused (no importers)? | `project?` | `semidx dead-code` |
| **diff** | What code *symbols* (not lines) changed between two git refs? | `ref_range` (`ref1..ref2` or `ref1...ref2`) | `semidx diff main..feat/x` |

> **Standalone/local only.** These tools run against the local index. Against a
> remote semidx server they return a "standalone/local mode only" message; index
> locally (`semidx index --project .`) and re-run.

## When to reach for which tool

**callers** — before deleting, renaming, or changing the signature of a function
/type, or when you need to know "is anything using this?". It lists the files
that import the symbol's package (direct + transitive).

**impact** — the stronger version of callers for refactors: the *transitive*
reverse-dependency closure, with each affected file tagged by depth. Run this
**first** when a change is non-trivial. `depth` bounds how far the ripple is
traced; raise it for wide refactors, lower it for a quick check.

**explain** — when you land on an unfamiliar symbol and want fast structural
context (what it depends on, who imports it, where its tests are) without reading
the whole package. Cheaper orientation than opening every file.

**deadcode** — when pruning, or when you suspect a symbol is unused. Results are
classified:
- `confirmed` — unexported, no importers (safe to delete).
- `public-api` — exported, no importers (review first — it may be an external API).

**diff** — when reviewing a branch or PR: it reports *new / removed /
changed-signature symbols* between refs, so you see the API/structure delta, not
just line noise. Use `..` (changes in ref2 since ref1) or `...` (changes ref2
introduced since divergence).

## How to read the results

- **callers/impact** return file paths (and depth, for impact). They list *files
  that import the package*, not precise call expressions — open the file and grep
  for the symbol name to confirm the exact call site. Think "candidate blast
  radius", then verify.
- **impact depth** is a relevance hint: depth-1 files are most likely to break;
  deeper files are progressively more insulated. Prioritize review by depth.
- **deadcode** can be wrong for reflection, interface satisfaction, or external
  consumers. `public-api` findings are deliberately conservative — don't delete
  exported symbols just because no internal file imports them.
- **diff** only sees Go symbols today (functions, types, methods, consts, vars).
  Non-Go changes won't appear.

## A typical safe-refactor sequence

1. `semantic_impact file=<path> line=<n>` — see the blast radius. If it's large,
   scope your change narrower or plan tests for the depth-1 files.
2. `semantic_explain file=<path> line=<n>` — confirm deps and find the tests.
3. Make the change.
4. `semantic_diff ref_range=main..HEAD` — confirm you changed exactly the symbols
   you intended, no surprise signature drift.
5. Run the tests found in step 2.

## vs grep and semantic search

- **grep** finds literal call sites *within a file you name* — but cannot tell
  you which files depend on a package without scanning everything. Use it to
  confirm exact call sites after `callers`/`impact` point you at the files.
- **semantic_search** finds code by meaning — use it to *locate* the symbol when
  you only know the behavior. Then switch to `callers`/`impact` for the
  relationships around it.
- These tools need an **indexed** project (`semantic_status` / `semidx status`).
  If the index is stale, callers/impact can miss recently-added files — re-index
  after big changes (`semantic_reindex` or `semidx index`).

## Anti-patterns

- Don't trust `deadcode` blindly for exported symbols — reflection, generated
  code, and external consumers are invisible to import analysis.
- Don't skip `impact` on a "small" signature change — a tiny change can have a
  huge depth-1 caller set. Always check before editing public symbols.
- Don't pass absolute paths; `file` is project-relative (the form the index
  stores), e.g. `internal/auth/token.go`.
