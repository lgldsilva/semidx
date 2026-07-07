---
name: auto-index
description: Automatically checks whether the current project is indexed by semidx and decides whether to use semantic search or grep fallback. Runs at session start, before major coding tasks, and whenever search results are inconclusive.
---

# Auto-index — smart search routing for AI agents

This skill teaches an AI agent to automatically detect whether the project
it's working in is indexed by semidx, and to choose the best search strategy
(semantic, grep, or both) for every query.

## New project flow (step by step)

When you enter a project for the first time, follow this decision tree:

```
Enter new project
    │
    ▼
semidx status --project .
    │
    ├── "ready" + total_files > 0
    │       └── ✅ Use semantic_search for intent queries
    │           Use grep for exact-string queries
    │
    └── "not found" OR total_files = 0
            │
            ├── Files ≤ 500?  →  Ask user: "Index now (fast, ~30s)?"
            │       ├── Yes  →  semidx index --project .  (blocks, then search)
            │       └── No   →  grep for now, index later
            │
            └── Files > 500?  →  Index in background + grep immediately
                    ├── semidx index --project . &
                    ├── Use grep while indexing runs
                    └── After indexing: switch to semantic_search
```

**The guiding principle**: never make the user wait. If indexing is fast
(≤500 files, ~30s), ask and block. If slow (>500 files, minutes), start it
in the background and use grep immediately.

---

## 1. Session start — check status

**Every time you enter a project** (or at the start of a session), run:

```bash
semidx status --project .
```

Or use the MCP `semantic_status` tool if the semidx MCP server is configured.

### Interpret the output

| Output | Meaning | Action |
|---|---|---|
| `"ready"`, files > 0 | Indexed and current | Use `semantic_search` |
| `"not found"` | Never indexed | See §2 below |
| `"ready"`, files = 0 | Registered, no content | See §2 below |
| `"indexing"` | Indexing in progress | Use grep; check again later |
| Command fails / MCP unavailable | No semidx available | Use grep exclusively |

---

## 2. Project NOT indexed — what to do

### Small projects (≤ 500 files): ask, then index if accepted

```
semidx status --project .
# ... "not found" ...

# Count files to estimate time:
find . -type f -not -path './.git/*' | wc -l
```

> "This project has N files and isn't indexed yet. Index it now with semidx
> (takes ~30s, enables semantic search) or continue with grep?"

If the user says yes:

```bash
semidx index --project .
```

If the user says no or doesn't respond: use `rg`/`grep` for all queries.

### Large projects (> 500 files): index in background, use grep now

> "This project has N files. Indexing will take a few minutes — I'll start it
> in the background. Use grep/ripgrep in the meantime."

```bash
# Start indexing in background:
semidx index --project . &
# Note the PID (optional — semidx handles its own state).

# Continue working with grep while indexing:
rg "pattern" --type go
```

After a few minutes, check status:

```bash
semidx status --project .
```

Once status shows `"ready"`, switch to `semantic_search` for intent queries.

### Remote / server setups

When `semidx login` is configured (remote server), use `semidx push` instead:

```bash
semidx push --project .
```

For large projects with timeout concerns, add `--batch-size`:

```bash
semidx push --project . --batch-size 30
```

For indexing a different branch as a separate project:

```bash
semidx index --project . --branch develop
# Creates project "repo@develop" — search it separately.
```

---

## 3. Project IS indexed — route queries smartly

### Use `semantic_search` for intent queries

Any query about *what* code does, *where* a concept lives, or *how* something
works — use semantic search:

```bash
semidx sgrep --project . --query "verify auth token"
semidx search --project . --query "how are database migrations applied"
```

Or the MCP `semantic_search` tool with the project name.

Examples of intent queries:
- "Where do we validate tokens?"
- "How is retry handled?"
- "What's the rate limiting logic?"
- "Find the input validation for uploads"

### Use `grep` / `rg` for exact-string queries

When you know the exact symbol, error message, function name, or path:

```bash
rg "fmt.Errorf" --type go
grep -rn "SOME_CONSTANT" .
```

Examples of exact queries:
- "Find all calls to `fmt.Errorf`"
- "Where is `MAX_RETRIES` defined?"
- "Count occurrences of `context.Background()`"

### When semantic search returns nothing useful

If `semantic_search` returns 0 results or low-confidence scores:

> "No good semantic matches. The index may be stale — try with grep:
> `rg '<query>' .`, or run `semidx push` / `semidx index` to refresh."

Then follow up with the grep command yourself.

---

## 4. Periodic staleness check

**Before major coding tasks** (refactoring, large changes, auditing symbols),
check whether the index is current:

```bash
semidx status --project .
```

If the file count is significantly lower than the project's actual files,
the index is stale:

> "The index may be stale (N files indexed vs M files in the project).
> Run `semidx index --project .` to refresh."

This is especially important after:
- A `git pull` or branch switch
- Adding or deleting many files
- Before auditing or renaming a symbol

---

## 5. Multi-branch projects

If the project has active development on multiple branches, index each branch
as a separate project:

```bash
# In worktree for main:
semidx index --project . --branch main      # → project "repo@main"

# In worktree for develop:
semidx index --project . --branch develop   # → project "repo@develop"
```

Search each branch project separately. If a result exists in another branch,
the project name in the search output tells you which branch it's from
(e.g., `repo@develop/src/auth.go:42`).

---

## Relationship with `semantic-search`

This skill is the **entry point** that decides *whether* to use semidx at all.
The `semantic-search` skill covers the detailed usage of semidx commands, MCP
tools, score interpretation, and keyword fallback — refer to it when you've
determined semantic search is the right tool.

---

## Anti-patterns

- Don't silently index a large project without asking the user first.
- Don't block the agent waiting for indexing when grep can answer immediately.
  Always offer grep as a parallel option.
- Don't use semantic search for exact-string queries — grep is faster and
  complete there.
- Don't skip the staleness check before critical operations — stale indexes
  produce misleading results.
- Don't retry the same failing semidx command in a loop; fall back to grep
  after one failure.
- Don't assume the index covers all branches — only the checked-out branch is
  indexed (unless `--branch` was used).
