# agent-ab — does semidx help a coding agent?

Reproducible harness behind
[`docs/research/agent-ab-semantic-search.md`](../../docs/research/agent-ab-semantic-search.md).

`run.sh [commit] [work-dir]` does the whole run:

1. It checks out `commit` (default `HEAD`) into a temporary worktree, builds
   `semidx` and removes `AGENTS.md` and `CLAUDE.md`.
2. It indexes that tree into an isolated SQLite index with a local Ollama
   embedding model.
3. It runs `semidx bench retrieval` in the keyword, vector, hybrid and
   hybrid-graph modes, plus a single-shot ripgrep baseline (`rg_baseline.py`).
4. It runs headless Claude Code (`claude -p`) on every gold query under three
   conditions (`grep`, `semidx`, `semidx-only`), then summarises the results
   with `analyze.py`.

## Requirements

- Go, `rg`, `python3` (standard library only).
- Ollama at `AB_OLLAMA_URL` (default `http://localhost:11434`) with the
  embedding model pulled (default `bge-m3`).
- An authenticated `claude` CLI. The agent phase uses real model tokens:
  about US$ 4–5 with Sonnet for 150 runs.

## Knobs

| variable | default | meaning |
|---|---|---|
| `AB_MODEL` | `sonnet` | agent model alias |
| `AB_EMBED_MODEL` | `bge-m3` | embedding model for the index |
| `AB_OLLAMA_URL` | `http://localhost:11434` | Ollama endpoint |
| `AB_WORKERS` | `4` | concurrent agent runs |
| `AB_SKIP_AGENTS` | `0` | `1` = retrieval tier only (no model spend) |

## Isolation

- semidx runs with its own `HOME` and index under the work dir, so it never
  reads the operator's config.
- Agent runs use `--setting-sources project --strict-mcp-config
  --no-session-persistence`, so user hooks, plugins and MCP servers do not
  leak into the measurement.
- The user-level `CLAUDE.md` still loads. It adds the same overhead to every
  condition.

## Output

- `agents.jsonl` has one line per run with:
  - `files`: the agent's answer;
  - `ndcg`, `mrr`, `recall` and `top1` (top-1 is a grade-3 file);
  - `turns`, `in_tok`, `out_tok`, `cost` and `wall_s`;
  - `calls`: tool name → count, which shows whether semidx was actually used.
- `run_agents.py` resumes from the runs already recorded in `agents.jsonl`.
- The 2026-11-10 artifacts are in `testdata/eval/agent-ab/2026-11-10/`.
