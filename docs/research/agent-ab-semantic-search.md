# Does semantic search help coding agents? An A/B on semidx

**Date:** 2026-11-10 · **Commit under test:** `0604bd9b` (retrieval also
re-run with the #126 graph fix) · **Harness:** `scripts/agent-ab/` · **Raw
data:** `testdata/eval/agent-ab/2026-11-10/`

## Question

semidx exists to give AI agents intent-level code search. Agents already
search with grep/glob/read loops. Does adding semidx make them find the right
code more often, or faster, or more cheaply?

## Setup

- **Corpus:** this repository (749 files, 8098 chunks), with `AGENTS.md` and
  `CLAUDE.md` removed. Without them the repository looks unfamiliar to the
  agent, which is semidx's best case.
- **Labels:** the versioned gold set `testdata/eval/semidx-retrieval-v1.json`
  has 50 queries with graded relevant files. The queries cover six intents:
  behavior, symbol, flow, documentation, ambiguous and project-resolution.
- **Index:** standalone SQLite, `bge-m3` on a local Ollama (GTX 1070).
  Indexing took 2 min 53 s. All semantic runs used `--strict-semantic` and
  none fell back to keyword.
- **Retrieval tier:** `semidx bench retrieval` in four modes. The baseline is
  a single-shot ripgrep ranker (`rg_baseline.py`). It splits the query into
  terms and ranks files by `idf·log(1+count)`, with no index and no iteration.
- **Agent tier:** headless Claude Code (`claude -p`, Sonnet), isolated from
  user hooks, plugins and MCP. For each query the task is "find the files that
  implement X; answer with up to 5 paths". Answers are scored with nDCG@5,
  MRR, recall@5 and top-1, where top-1 means the first answer is a grade-3
  file. There were three conditions × 50 queries = 150 runs, and none errored:
  - `grep`: Read, Grep and Glob only.
  - `semidx`: the same tools plus the semidx MCP, with a system-prompt hint
    that it is available.
  - `semidx-only`: Read plus the semidx MCP. Grep and Glob are removed, so
    semidx is the only search tool.

## Results

### Retrieval (top-10, 50 queries)

| system | nDCG@10 | MRR | Recall@10 | p50 latency |
|---|---|---|---|---|
| ripgrep, single shot, no index | 0.460 | 0.426 | **0.670** | ms |
| semidx keyword | 0.397 | 0.357 | 0.543 | 29 ms |
| semidx vector | 0.467 | 0.447 | 0.553 | 847 ms |
| semidx hybrid | **0.480** | **0.459** | 0.603 | 826 ms |
| semidx hybrid-graph, before #126 | 0.381 | 0.418 | 0.397 | 896 ms |
| semidx hybrid-graph, with #126 | 0.480 | 0.459 | 0.603 | 915 ms |

nDCG@10 by intent:

| system | ambiguous | behavior | docs | flow | resolution | symbol |
|---|---|---|---|---|---|---|
| ripgrep | 0.266 | 0.527 | 0.586 | 0.451 | 0.224 | 0.520 |
| semidx vector | 0.177 | 0.457 | **0.744** | 0.519 | 0.237 | 0.565 |
| semidx hybrid | 0.082 | 0.629 | 0.733 | 0.469 | 0.333 | 0.406 |

### Agents (150 runs)

| condition | nDCG@5 | MRR | R@5 | top-1 | turns | input tok | cost USD (50 runs) | wall s | used semidx |
|---|---|---|---|---|---|---|---|---|---|
| grep | 0.895 | 0.932 | 0.867 | 0.84 | 4.3 | 26,965 | 0.99 | 12.2 | — |
| semidx (available) | 0.896 | 0.934 | 0.863 | 0.88 | 5.0 | **56,396** | 1.25 | 14.0 | **0/50** |
| semidx-only | **0.945** | **0.970** | **0.897** | **0.96** | **2.7** | 38,000 | **2.03** | 11.9 | 50/50 |

nDCG@5 by intent:

| condition | ambiguous | behavior | docs | flow | resolution | symbol |
|---|---|---|---|---|---|---|
| grep | 0.866 | 0.952 | 0.749 | 0.911 | 0.686 | 0.983 |
| semidx | 0.798 | 0.969 | 0.519 | 0.902 | 0.932 | 1.000 |
| semidx-only | 0.698 | 0.971 | **0.994** | 0.943 | **1.000** | 0.979 |

Paired by query, against `grep`:

- `semidx`: mean nDCG@5 difference +0.001, with 9 wins, 11 losses and 30 ties.
- `semidx-only`: mean difference +0.051, with 14 wins, 8 losses and 28 ties.
  A sign test gives p ≈ 0.29, so this gain is not statistically significant.

## Findings

1. **When semidx is optional, the agent never uses it.** In 0 of 50 runs did
   the agent call semidx. It made 163 Grep, 20 Glob and 17 Read calls instead.
   The only effect of offering the tool was cost: about 20 extra MCP tool
   definitions doubled input tokens (+109%) and raised cost 26%. Accuracy did
   not change.
2. **When semidx is the only search tool, results are somewhat better, at
   double the cost.** Top-1 rises from 84% to 96% and turns drop from 4.3 to
   2.7. Total cost doubles (US$ 2.03 vs 0.99), most likely because every hit
   returns full chunk content that the prompt cache cannot reuse. The gain is
   not significant over 50 queries.
3. **The gains are concentrated in prose and concept questions.** For
   documentation (0.99 vs 0.75) and project-resolution concepts (1.00 vs
   0.69), semidx-only clearly beats grep. For ambiguous short queries
   (0.70 vs 0.87) it is clearly worse. Symbol and behavior lookups are already
   near the ceiling with grep.
4. **Retrieval alone does not beat a naive grep.** Hybrid nDCG@10 is 0.480
   against 0.460 for ripgrep, and ripgrep has better recall (0.67 vs 0.60).
   The keyword mode is worse than ripgrep. The earlier versioned keyword
   baseline (`baselines/96d1c46-keyword.json`, nDCG 0.058) predates the
   current indexer and is not comparable.
5. **#126 fixed a real quality regression.** The graph merge used to push
   direct hits down, and hybrid-graph went from 0.381 to 0.480 once it was
   fixed.

## Threats to validity

- There is a single, medium-sized repository, and it is semidx itself. The
  queries were written by people who know the code, and there was one run per
  condition and query. Results are noisy at this n.
- Scenarios where semidx has a structural advantage were not tested:
  - very large monorepos, where grep loops get expensive;
  - discovery across many repositories, where the agent does not know where
    to look;
  - corpora that are not code (PDF, Office, EPUB), which grep cannot read.
- Only one model (Sonnet) and one embedding model (bge-m3) were used.

## Recommendations

- Do not position semidx as a general replacement for grep in code search.
  As an optional MCP tool it adds cost and no value.
- If the product continues, focus it on the niche the data supports:
  documents and concept questions, and discovery across repositories.
- If the product continues, change the integration:
  - Expose one search tool instead of ~20, so the context does not grow.
  - Return compact hits (path, lines and a short snippet) instead of full
    chunks.
  - Route documentation and concept queries to it explicitly.
- Before investing further, re-run this harness on a document corpus and on a
  multi-repo set.

## Reproduce

```sh
scripts/agent-ab/run.sh <commit> [work-dir]   # full run, ~US$ 4-5 of model usage
AB_SKIP_AGENTS=1 scripts/agent-ab/run.sh       # retrieval tier only, no model spend
python3 scripts/agent-ab/analyze.py testdata/eval/agent-ab/2026-11-10/agents.jsonl
```

See `scripts/agent-ab/README.md` for requirements and knobs.
