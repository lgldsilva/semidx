---
name: impact-before-refactor
description: Reflex before changing, renaming, deleting, or altering the signature of any function, method, type, or exported symbol — run semidx impact/callers first to see the blast radius (who depends on it) and avoid blind regressions. Also run diff after the change to confirm you touched exactly the intended symbols. Triggers on refactors, signature changes, deletions, "is it safe to change/remove X", public-API edits, and any edit where unseen cross-file callers could break. Use whenever you are about to edit code you did not personally write or where the caller set is not obvious.
---

# Impact-check before you refactor

Agents cause regressions when they edit a symbol blind to its callers. Before
you change a function/method/type signature, rename, or delete **any** symbol —
especially one you didn't write — check the blast radius first. It takes one
tool call and prevents most refactor regressions.

## The reflex (do this every time)

1. **Before editing** a symbol at `file:line`, call `semantic_impact`:
   ```
   semantic_impact  file=<path>  line=<n>  project?=<p>  depth?=5
   ```
   This returns every file that transitively depends on that symbol's package,
   tagged by depth. **Depth-1 files will most likely break** — those are your
   review/test targets.

2. If the impact set is **large** (many depth-1 files), either:
   - narrow the change (add instead of modify; keep the old signature as a
     shim), or
   - explicitly plan to update/test each depth-1 file.

3. If `semantic_impact` is unavailable, fall back to `semantic_callers`
   (direct importers) — partial information beats none.

4. **After editing**, call `semantic_diff` to confirm you changed exactly the
   symbols you intended (no accidental signature drift, no stray deletions):
   ```
   semantic_diff  ref_range=main..HEAD
   ```

## Decision rules

| Situation | Action |
|---|---|
| 0 depth-1 callers | Safe to change; proceed. |
| 1–3 depth-1 callers | Safe-ish: update each caller in the same change. |
| many depth-1 callers | High risk: prefer additive change, or split into a deprecation path. |
| exported (`public-api`) symbol | Even with 0 internal callers, it may have **external** consumers — never delete blindly; deprecate instead. |

## Why not just grep?

`grep` only finds literal call sites in files you already think to search, and
cannot tell you which files *import the package* without scanning the whole tree.
`semantic_impact` uses the indexed dependency graph and returns the full
transitive set in one call. Use grep *after* impact, to pinpoint the exact call
expression inside each affected file.

## Prerequisite

The project must be indexed. If `semantic_impact` returns "no indexed project"
or looks stale, index first (`semidx index --project .` / `semantic_reindex`)
and re-run. These tools are **standalone/local only** — against a remote server
they return an in-band "standalone only" message.

## Example

You're about to change `parseToken` in `internal/auth/token.go:42`:

1. `semantic_impact file=internal/auth/token.go line=42 depth=5`
   → returns 7 affected files (3 at depth 1: `middleware.go`, `handler.go`,
     `grpc.go`).
2. That's a meaningful caller set — open those three, grep for `parseToken`,
   and update them in the same change.
3. `semantic_diff ref_range=main..HEAD`
   → confirms you changed `parseToken` and its 3 callers, nothing else.

One reflex, one tool call, no blind regressions.
