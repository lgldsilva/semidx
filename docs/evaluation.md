# Retrieval evaluation

semidx evaluates retrieval changes with a versioned gold set, serialized run
metadata, compatibility guards, and explicit regression thresholds.

## Evidence flow

```text
frozen corpus + dataset
          |
          v
  bench retrieval
          |
          v
 versioned result artifact
          |
          +---- baseline
          |
          +---- candidate
                    |
                    v
          bench compare --fail-if
                    |
                    v
              pass or fail
```

The committed PR 00 baseline uses keyword mode because it is hermetic and does
not require an embedding provider. A separate SQLite integration test uses a
deterministic test embedder to verify isolated vector retrieval and exact
repeatability. Real-provider vector runs are operational evidence, not merge
gates.

## Versioned inputs

| Artifact | Purpose |
|---|---|
| `testdata/eval/semidx-retrieval-v1.json` | Gold set with 50 graded queries |
| `testdata/eval/baselines/96d1c46-keyword.json` | Keyword baseline for commit `96d1c46` |
| `testdata/eval/thresholds/retrieval-default.json` | Candidate regression limits |
| `testdata/eval/retrieval-smoke.json` | Fast five-query CLI smoke dataset |

The gold set contains the required distribution:

| Intent | Queries |
|---|---:|
| Behavior | 15 |
| Symbol | 10 |
| Cross-file flow | 10 |
| Documentation/configuration | 5 |
| Ambiguous/negative | 5 |
| Project resolution | 5 |

## Validate the dataset

Run structural validation and verify every judged path against the frozen
corpus:

```bash
semidx bench validate-dataset \
  --dataset /path/to/semidx-retrieval-v1.json \
  --project /path/to/corpus
```

Validation rejects unsafe paths, duplicate IDs/files, invalid grades and line
ranges, missing relevance judgments, and missing corpus files.

## Generate a result artifact

Run the command from the corpus checkout because the versioned dataset uses
`project_ref: "."`:

```bash
cd /path/to/corpus

export SEMIDX_LOCAL_INDEX=/tmp/semidx-eval.db
semidx --local --keyword index .

semidx --local --keyword bench retrieval \
  --dataset /path/to/semidx-retrieval-v1.json \
  --mode keyword \
  --runs 5 \
  --seed 42 \
  --output candidate.json \
  --json
```

Supported retrieval modes:

| Mode | Retriever contract |
|---|---|
| `keyword` | Keyword only; no embedding call |
| `vector` | Vector only; no routing, fusion, or keyword fallback |
| `hybrid` | Vector plus keyword fusion |
| `hybrid-graph` | Hybrid retrieval followed by graph expansion |

`vector` is intentionally unavailable through the remote API until the server
exposes an isolated vector retriever. Use local SQLite or PostgreSQL.

## Compare with the baseline

```bash
semidx bench compare \
  testdata/eval/baselines/96d1c46-keyword.json \
  candidate.json \
  --fail-if testdata/eval/thresholds/retrieval-default.json
```

Comparison is rejected before threshold evaluation when artifacts differ in:

- dataset hash;
- corpus fingerprint;
- backend;
- model or dimensions;
- project identity;
- retrieval mode;
- seed or run count.

The absolute worktree path and environment remain in metadata for diagnostics,
but do not invalidate an otherwise identical corpus checked out elsewhere.

## Artifact metadata

Every retrieval result records:

| Field | Why it is required |
|---|---|
| Commit | Identifies the evaluated implementation/corpus snapshot |
| Dataset SHA-256 | Prevents comparing different judgments |
| Backend | Separates SQLite, PostgreSQL, and remote behavior |
| Model and dimensions | Prevents comparing incompatible embeddings |
| Project identity and worktree | Proves which project checkout was resolved |
| Corpus fingerprint | Proves that indexed paths and hashes match |
| Mode | Separates keyword, vector, hybrid, and graph retrieval |
| Seed and runs | Makes repetition policy explicit |
| Environment | Diagnoses platform-dependent latency |
| Fallback count | Exposes degraded semantic runs |
| p50/p95/p99 latency | Makes performance changes observable |

Quality metrics are aggregated per query using the median across repeated runs.
Latency percentiles use every successful observation.

## Interpretation limits

- The PR 00 baseline is a measurement reference, not a quality target.
- Keyword results from two newly built indexes may still differ on tied rows
  until ranked lexical retrieval lands in PR 01. Repeated runs against the same
  frozen index must remain identical.
- Real embedding providers may change model revisions outside this repository.
  Use the deterministic SQLite integration test for the merge gate.
- A passing threshold file means the configured regressions were not exceeded;
  it does not prove that the absolute retrieval quality is sufficient.
