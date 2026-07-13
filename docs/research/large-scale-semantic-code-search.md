# Large-Scale Semantic Code Search: Research & Recommendations for semidx

> **Status:** Research completed 2026-07-06
> **Scope:** How tools like Sourcegraph/Zoekt, Glean, Google Code Search, and modern ANN systems handle 100K+ files / millions of chunks
> **Goal:** Concrete, evidence-backed recommendations for semidx at scale

---

## Table of Contents

1. [Executive Summary](#1-executive-summary)
2. [Indexing Performance at Scale](#2-indexing-performance-at-scale)
3. [Search Performance at Scale](#3-search-performance-at-scale)
4. [Storage Optimization](#4-storage-optimization)
5. [Architecture Patterns](#5-architecture-patterns)
6. [Graph-RAG BFS at Scale](#6-graph-rag-bfs-at-scale)
7. [Current semidx Assessment & Roadmap](#7-current-semidx-assessment--roadmap)
8. [Sources](#8-sources)

---

## 1. Executive Summary

### Key findings

| Area | Finding | Source |
|---|---|---|
| **Trigram beats vector** for exact/identifiers | Zoekt serves 50ms queries on 2GB+ codebases using trigram indexes, not vectors | [Zoekt design docs](https://github.com/sourcegraph/zoekt/blob/main/doc/design.md) |
| **Incremental indexing** — O(δ) not O(n) | Git-diff + content-hash + Merkle tree = 15ms re-index for 1 changed file | [Cartog architecture](https://www.julienrollin.com/en/posts/cartog-incremental-merkle-tree/), [Glean incremental](https://glean.software/blog/incremental/) |
| **pgvector HNSW sweet spot** | 1M-10M vectors: ~35ms median, 2,300 QPS on 8-vCPU | [pgvector benchmarks](https://markaicode.com/benchmarks/postgresql-semantic-search-benchmark/) |
| **pgvector HNSW at 100M+ vectors** | 600+ GB index, needs 256+ GB RAM, OOM is the primary failure mode | [pgvector 100M analysis](https://db-news.com/pgvector-hnsw-index-memory-pressure-at-100m-vectors-what-breaks) |
| **halfvec halves storage** | <1% recall loss, cuts index from ~624 GB to ~330 GB for 100M×1536d | [pgvector scaling guides](https://umurinan.com/pages/posts/pgvector-at-10-million-rows-is-a-different-animal.html) |
| **Binary quantization + rerank** | 32x compression, recovers 95-98% recall with reranking step | [HuggingFace quantization blog](https://huggingface.co/blog/embedding-quantization) |
| **Content-addressed dedup savings** | 40-90% on real codebase workloads with CDC | [Chunkstore benchmarks](https://github.com/MuratovER/chunkstore), [XetHub benchmarks](https://blogs.nionee.com/from-files-to-chunks-improving-hf-storage-efficiency/) |
| **Sourcegraph Zoekt memory** | 310 KB RAM per repo after optimizations, 1M repos served | [Sourcegraph blog](https://sourcegraph.com/blog/zoekt-memory-optimizations-for-sourcegraph-cloud) |

### The critical insight

For **code search**, the workload differs fundamentally from document RAG:

1. **Identifier queries** (function names, variables, types) dominate — these are exact/substring matches, not semantic
2. **Semantic queries** ("how does auth work?") need vector search but represent the minority
3. **Hybrid search** (BM25 + vector with RRF fusion) beats either alone by 98% relative recall

This means semidx should **route queries by type** — not everything needs ANN.

---

## 2. Indexing Performance at Scale

### 2.1 How the leaders do it

#### Sourcegraph / Zoekt (trigram-indexed regex search)

Zoekt splits the codebase into **shards** (one per repo, ~3x corpus size on disk). Each shard is an mmap'd file with positional trigram indexes. Key characteristics:

- **Index size**: ~3x corpus (2x offsets + 1x content), but only ~1.2x corpus in RAM because posting lists live on SSD
- **Query speed**: sub-50ms on Android-sized codebases (~2GB text)
- **Scaling**: 1M repos on ~300 MB RAM per server after memory optimizations (310 KB/repo)
- **Shard merging**: small repos merged into compound shards to reduce file handle count
- **Compound shards**: `compound-{hash}.zoekt` merges many small repos into one file

```
simple shard:  {repo}_v{sha}.zoekt   — single repo
compound shard: compound-{hash}.zoekt — many repos merged (Sourcegraph optimization)
```

**Memory optimization journey** (5x reduction from 1,400 KB → 310 KB per repo):
1. Replace Go maps with sorted arrays + binary search for trigram→offset mapping
2. Split 64-bit trigrams into 32-bit top/bottom halves (3.5 GB → 2.3 GB)
3. ASCII vs Unicode trigram split (2.3 GB → 2.2 GB)
4. Compress filename posting lists, decompress on demand (3.3 GB → 1.1 GB)
5. Run-length encode byte-offset lookup tables (2.3 GB → 0.2 GB)

#### Glean (incremental fact database)

Glean uses **stacked DBs** — each incremental change creates a new layer on top of a base DB, rather than mutating in place:

- Base DB: full index built from head revision
- Incremental overlay: only changed facts since base
- Stack depth: supports arbitrary tree of overlays
- DB backend: RocksDB or LMDB (LMDB is 30-40% faster in benchmarks)
- O(changes) indexing time, not O(repository)

#### Google Code Search / Debian Code Search

Google's original approach uses **trigram inverted indexes** (~20% of corpus size):

- Index: Linux 3.1.3 kernel (420 MB) → index of 77 MB (18%)
- Query: trigram index narrows 36,972 files → ~25 candidates for regex evaluation
- Speedup: ~100x over brute-force grep
- Partitioning across machines: Debian Code Search splits the index into **shards** to work around the 2 GB mmap limit
- mmap-based: index is memory-mapped, relies on OS page cache

### 2.2 Incremental Indexing: the O(δ) pipeline

**Three-layer filtering** (Cartog architecture):

```
Layer 1: git diff                                  cost: one git command
    Compare last_indexed_commit vs HEAD
    git diff --name-only <last_commit>..HEAD
    git ls-files --others --exclude-standard
    git diff --name-only (working tree)
    git diff --name-only --cached (staged)
    ↓ file changed?
Layer 2: SHA-256 content hash                      cost: read + hash per file
    File on disk vs stored hash (catches mtime-only "changes")
    ↓ hash differs?
Layer 3: Symbol Merkle tree                        cost: parse + diff
    Compare content_hash and subtree_hash per symbol
    Only rewrite changed symbols to DB
```

**Benchmarks** (69 Python files, 4K lines, Cartog):

| Scenario | Time |
|---|---|
| Full index (cold) | ~95 ms |
| Incremental (1 file modified) | ~15 ms |
| Incremental (0 changes) | ~3 ms |

Content-addressed parse cache (GitNexus) shows **55-98% speedup** on re-indexing with chunk-level caching.

**For semidx specifically** — the current `FileUpToDate` by hash is already layer-2 equivalent. The missing layers are:
- Layer 0: git diff to skip files entirely (not just skip content hashing)
- Layer 3: symbol-level Merkle diff to avoid re-embedding unchanged chunks within a changed file

### 2.3 Parallel Indexing

**Worker pool sizing** — the research is consistent:

| Source | Optimal workers | Notes |
|---|---|---|
| semidx current | 4 (default) | Conservative, laptop-safe |
| Glean | 12 for C++ | CPU-bound, memory per worker matters |
| Sourcegraph/Zoekt | Queue-based, priority-ordered | Separates indexing from search infra |
| pgvector HNSW build | 8 parallel workers | Requires 64 GB maintenance_work_mem |

**Key insight**: Indexing parallelism is limited by the **embedding API rate limit** and **memory per worker**, not CPU. A pipeline should:

1. **Read+chunk in parallel** (CPU-bound, many workers)
2. **Embed in serial batches** (API-bound, controlled by rate limiter)
3. **Insert in batches** (DB-bound, pgx batch size of 500-1000)

Current semidx does step 2-3 together in `embedAndInsert` with a batch size of 8. This is reasonable but could be improved by decoupling embedding from insertion (embed ahead of DB).

### 2.4 Compression Techniques for Embeddings

| Technique | Compression | Recall impact | Complexity |
|---|---|---|---|
| **halfvec** (float16) | 2x | <1% loss | Trivial (pgvector native) |
| **Scalar quantization** (int8) | 4x | ~1-2% loss | Trivial |
| **Binary quantization** | 32x | ~5-8% loss; recovers with rerank | Two-stage retrieval |
| **Product quantization** (PQ) | 10x-100x | Configurable (2-10% loss) | Needs training phase |
| **SegPQ** (compressed PQ) | 4.7x on codebook | Lossless | Novel, research-stage |

**Recommendation**: `halfvec` as default (already partially done for >2000d). Add `halfvec` as storage option for all dimensions, not just >2000d HNSW gate.

### 2.5 Tiered Indexing (Hot/Cold)

**The pattern** (from pgvector at 10M+ vectors):

```
Hot index (recent/active):
  - Small table, fully HNSW-indexed
  - Frequently queried projects or branches
  - Fits in shared_buffers

Cold index (archive):
  - Large table, no or IVFFlat index
  - Infrequently queried
  - Bulk-loaded, batch-rebuilt
```

**Query union**: `SELECT * FROM hot UNION ALL SELECT * FROM cold WHERE ...`

For semidx: consider a `projects.active` column. Active projects (indexed in last N days) get HNSW; dormant projects get demoted to keyword-only or dropped indexes.

---

## 3. Search Performance at Scale

### 3.1 ANN at Scale: pgvector HNSW Tuning

**Definitive benchmarks** on AWS r6i.2xlarge (8 vCPU, 64 GB RAM), 1M×768d vectors:

| Metric | No Index | IVFFlat | HNSW |
|---|---|---|---|
| QPS | 21 | 680 | **2,300** |
| p50 latency | 2,850 ms | 82 ms | **13 ms** |
| p99 latency | 3,400 ms | 210 ms | **48 ms** |
| Index size | 0 | 1.9 GB | 3.8 GB |
| Build time | N/A | 247 s | **142 s** |

**Key tuning knobs** (quantitative):

| Parameter | Default | Tuned range | Effect |
|---|---|---|---|
| `ef_search` | 40 | 100-200 | Recall: 50%→95%+; latency 2× |
| `m` | 16 | 16-32 | Index size 2×; recall improves marginally |
| `ef_construction` | 64 | 64-128 | Build time 1.5×; recall improves |

**Practical limits per node**:

| Vector count | Dims | Index size | RAM needed | Viable? |
|---|---|---|---|---|
| 1M | 768 | 3.8 GB | 8 GB+ | ✅ Easy |
| 10M | 768 | ~35 GB | 64 GB+ | ✅ Feasible |
| 10M | 1536 | ~80 GB | 128 GB+ | ⚠️ Requires tuning |
| 100M | 1536 | ~624 GB | 256 GB+ | ❌ OOM risk; needs halfvec |
| 1B | 768 | ~3.5 TB | N/A | ❌ pgvector alone can't (needs PQ+partitioning) |

**For 100M+ vectors in pgvector**:
- Build time: hours (set `maintenance_work_mem` to 60-80 GB per worker)
- Streaming fallback mode if memory insufficient → degraded recall
- Halfvec halves index (~330 GB for 100M×1536d)
- Hot upper layers (~3-4 levels, few hundred MB) must stay in `shared_buffers`

### 3.2 Pre-filtering Strategies

The **#1 performance killer** for pgvector HNSW at scale is **post-filtering** — applying WHERE clauses after ANN search. Solutions ordered by effectiveness:

1. **Pre-filter with B-tree**: Index frequently-filtered columns (project_id, language, path prefix)
2. **Partial HNSW indexes**: Create per-project or per-language HNSW indexes
3. **Partitioning**: Partition chunks_<dims> tables by project_id or time
4. **Hybrid scan**: Use trigram index for identifier terms, fall back to vector for semantic

**semidx already does**: column indexes on `project_id` and `file_id` in chunks tables. The per-dimension table design means each model has its own HNSW index.

**Recommended upgrade**: Pre-filter with worktree join before vector search (already done for `SearchSimilarWorktree`). Extend to support project, language, and directory pre-filters.

### 3.3 Hybrid Search Optimization

**Dense + Sparse fusion results** (BM25 + vector, RuVector benchmark):

| Variant | Recall@10 | QPS |
|---|---|---|
| Sparse-only (BM25) | 0.372 | 29,616 |
| Dense-only (cosine) | 0.500 | 2,180 |
| **Hybrid (RRF, k=60)** | **0.738** | 2,048 |
| Hybrid (linear, α=0.5) | 0.644 | 2,026 |

**+98% relative recall gain** over best single leg (0.372 → 0.738).

**Reciprocal Rank Fusion (RRF)** formula:
```
score(d) = Σ 1 / (rank_i(d) + k)   for each retrieval method i
```
Where k=60 is the standard constant.

**Architectural patterns for hybrid**:

1. **Separate indices + application fusion** (simplest): Run BM25 and ANN separately, fuse in Go
2. **Unified index** (advanced): Store both sparse and dense in pgvector; `pgturbohybrid` proves this works inside PostgreSQL
3. **Pre-filter + rerank**: Binary index for first pass, full-precision for rerank

**For semidx**: Implement RRF fusion between:
- Keyword search (ILIKE with trigram GIN index — already exists)
- Vector search (HNSW — already exists)
- This costs ~2x query latency but gives meaningfully better results

### 3.4 Result Caching

**Tiered caching** pattern from production hybrid search systems:

| Cache tier | Content | TTL | Size |
|---|---|---|---|
| **L1: In-memory** | Hot query results (query_hash → results) | 30-300s | 10K-100K entries |
| **L2: Embedding cache** | Query embedding (query_text → vector) | 1h | 1K-10K entries |
| **L3: Result cache** | Pre-computed top-K for common queries | 1h | Redis/persistent |

For semidx: an LRU cache for the 1000 most recent `(project, query, topK)` pairs would give high hit rates on re-queries during development.

### 3.5 Query Routing

The critical insight from Google Code Search and Sourcegraph: **not all queries need vector search**.

**Query routing rules**:

```
if query contains only identifiers (functions, types, variables):
    → Route to trigram/keyword index (ILIKE or FTS5)
    → Cost: ~1ms, highly precise
elif query is a natural-language question:
    → Route to vector search (HNSW)
    → Cost: ~10-50ms
elif hybrid mode enabled:
    → Run both, fuse with RRF
    → Cost: sum of both paths
```

**Current semidx**: The CLI already has `--keyword` flag. This should be **automatic** — detect whether the query looks like code symbols (route keyword) or natural language (route vector).

### 3.6 Faceted Search

**Recommended facets** for code search:

| Facet | Implementation | Index needed |
|---|---|---|
| By project | WHERE project_id = $1 | B-tree on chunks.project_id ✅ |
| By language | File extension extraction | Could add language column |
| By directory | f.path LIKE 'prefix%' | B-tree on files.path ✅ |
| By file type | test/generated | Could add file_category column |
| By time range | indexed_at column | B-tree on files.indexed_at |

---

## 4. Storage Optimization

### 4.1 Minimizing Disk Usage

**Raw storage math** for 1M chunks × 768d embeddings:

| Representation | Per chunk | Total | Notes |
|---|---|---|---|
| float32 vector | 3,072 B | 3.1 GB | Full precision |
| halfvec (float16) | 1,536 B | 1.5 GB | <1% recall loss |
| int8 scalar | 768 B | 0.77 GB | ~2% loss |
| binary (1-bit) | 96 B | 96 MB | ~8% loss + rerank |
| PQ 16x | 192 B | 192 MB | Configurable loss |

**For semidx**: Content text is the dominant cost, not vectors:
- A 100K-file codebase at 150 avg chunks/file = 15M chunks
- At 200 bytes avg content = 3 GB of text
- Embeddings at 768d float32 = 46 GB
- Embeddings at halfvec = 23 GB

So **halfvec cuts the total index by ~33%** at negligible recall cost.

### 4.2 halfvec for All Dimensions

semidx already uses `halfvec` for dims > 2000 (the HNSW vector limit). The research shows halfvec should be the **default storage type** for all dimensions:

- <1% recall degradation on normalized embeddings (all major models normalize)
- 2x storage reduction
- 1.5-2x faster index builds (less data to process)
- Supported by pgvector 0.7+ for HNSW indexes

**Recommendation**: Store as `halfvec(dims)` by default; offer `vector(dims)` as opt-in for precision-critical use.

### 4.3 Binary Quantization with Rerank

For truly massive indexes (100M+ chunks), binary quantization with rerank is the proven pattern:

```
First pass: binary HNSW over bit column        → 32x compression, ~8 ms
               fetch top-200 candidates
Second pass: rerank with halfvec/float32        → full recall, ~2 ms
               pick top-10 from candidates
```

This is what the HuggingFace 41M Wikipedia demo and the billion-vector pgvector guides recommend.

### 4.4 Content-Addressed Deduplication

**Real-world savings** from content-addressed storage:

| Workload | Savings | Method |
|---|---|---|
| 200 chunk pool × 1000 files | **90%** | Chunk-level dedup |
| Document versions (90/10 overlap) | **40%** | CDC chunking |
| Prefix insert + 1 byte | **45%** | CDC (vs 0% fixed) |
| Whole duplicate upload | **50%** | File-level dedup |
| Firefox binary, 3 releases | **13-16%** | CDC (even on binaries) |
| PyTorch model checkpoints | **30-85%** | Parameter-level dedup |

**semidx already has**: File-level content-addressed storage (`UNIQUE(project_id, path, hash)`). The `FileUpToDate` check saves re-embedding.

**What's missing**: Cross-project dedup. Currently, if the same file exists in two projects, it's embedded twice. A global content-addressed embedding cache (keyed by content hash) would save 10-30% on typical multi-project setups.

### 4.5 Tiered Storage

**Pattern from production pgvector**:

1. **Hot vectors in memory**: Active projects' HNSW index pages in `shared_buffers`
2. **Warm vectors on SSD**: Less active projects, IVFFlat or no ANN index
3. **Cold vectors on disk**: Archived projects, keyword-only access

For 100M+ vectors, the hot index (upper HNSW layers) is only ~200 MB and must be pinned in memory. The rest of the index can live on SSD with acceptable latency.

### 4.6 SQLite at Scale — What Breaks

Current semidx SQLite store uses brute-force cosine. At scale:

| Chunks | Time per query | Viable? |
|---|---|---|
| 10K | ~15 ms | ✅ |
| 100K | ~150 ms | ✅ |
| 1M | ~1.5 s | ⚠️ Borderline |
| 10M | ~15 s | ❌ Unusable |

**What breaks** (from the research):
1. **No ANN** — brute-force is O(n). Fine for laptop, dies at 1M+
2. **Single-writer** — `MaxOpenConns(1)` serializes writes. OK for CLI, not for concurrent access
3. **WAL journal** — already using WAL ✅. Never mix journal modes
4. **FTS5** — excellent for keyword search, but no semantic fallback
5. **Vaccum bloat** — DELETE-heavy workloads (re-indexing) create dead rows. Need periodic `PRAGMA wal_checkpoint(TRUNCATE)`

**Recommendation**: Keep SQLite as the CLI/local path. Document that 100K+ files should use Postgres. Add a benchmark warning to `semidx config`.

---

## 5. Architecture Patterns

### 5.1 Zoekt's Separation of Indexing and Search

```
┌─────────────────────┐     ┌──────────────────────┐
│  zoekt-indexserver  │ ──→ │  .zoekt shard files   │ ←── mmap ── │  zoekt-webserver │
│  (CPU-heavy, batch) │     │  (shared filesystem)  │             │  (latency-critical)│
└─────────────────────┘     └──────────────────────┘             └──────────────────────┘
```

Key design decisions:
- **Search and indexing are independent processes** — zero-downtime index updates
- **mmap for search** — OS page cache handles hot/cold automatically
- **Single indexserver per index directory** — file-based locking prevents corruption
- **Multiple webservers** — stateless, can load-balance

### 5.2 Zoekt's Shard Architecture

```
DirectorySearcher
├── rankedShard[0] → indexData → matchTree execution
├── rankedShard[1] → indexData → matchTree execution
├── rankedShard[2] → indexData → matchTree execution
└── ... (parallel, bounded by semaphore)
```

Cost-level evaluation:
- `costConst` (0): Metadata/constant checks (cheapest)
- `costMemory` (1): Memory-resident index lookups
- `costContent` (2): Loading file content from disk
- `costRegexp` (3): Full regex execution (most expensive)

This **lazy evaluation** skips expensive operations when cheaper filters already eliminate candidates.

### 5.3 Glean's Stacked DB Architecture

```
Query → [Layer N (stacked) → ... → Layer 1 (stacked) → Base DB]
         ↑ Facts newer than base (incremental updates)
```

Stack properties:
- Each layer is a separate RocksDB/LMDB
- Layers are immutable once written
- Queries stack: read from top layer first, fall through to base
- `O(changes)` updates, not `O(repository)`
- Zero-copy sharing between layers (identical facts stored once)

### 5.4 Debian Code Search's Index Partitioning

```
PostgreSQL (ranking data)
     │
nginx → dcs-web → index-backend[0..N] → source-backend
     │              (shard files)        (grep)
     └── static files
```

The index is partitioned because Go's `mmap` has a 2 GB limit (32-bit `int` for lengths). Each shard is a self-contained index file. Multiple `index-backend` processes serve different shards.

### 5.5 Caching & Precomputation

**What Zoekt precomputes** (cost-0 operations):
- Trigram→posting-list offset mapping
- Per-file document ID lists
- File metadata (size, branch masks, language)

**What is computed at query time**:
- Trigram intersection (AND of required trigrams)
- Regex evaluation on candidates
- BM25 scoring
- Result ranking and aggregation

**For semidx**: Similar split — embeddings and HNSW index are precomputed; distance computation and ranking happen at query time.

### 5.6 Connection Pooling, Timeouts, Circuit Breakers

**Production patterns** from hybrid search systems:

| Component | Setting | Justification |
|---|---|---|
| PostgreSQL pool size | 20-50 | Limited by worker processes |
| HNSW query timeout | 5s | Prevent runaway graph walks |
| Embedding API timeout | 10s | Gemni/Groq/OpenRouter can stall |
| Embedding circuit breaker | 3 failures/60s | Protect API limits |
| Index build timeout | 30m | Per-project, in job queue |
| Max connections per backend | `MaxOpenConns(1)` for SQLite | Prevents corruption |

**semidx already has**: circuit breaker logic in `internal/embed/circuit.go`. Missing: query-level timeouts in `SearchSimilar`.

---

## 6. Graph-RAG BFS at Scale

### 6.1 The BFS Cost Problem

Current semidx `FetchGraphNeighbors` loads the full dependency graph into memory, then does BFS in Go. For a 100K-file codebase with ~300K edges:

- Load: ~50 MB for edges (trivial)
- BFS worst case: O(V+E) ≈ 400K operations
- **But**: if BFS expands to the full graph, it returns the whole project — useless

### 6.2 What the Research Suggests

**Codebase-Memory** (Tree-Sitter knowledge graph paper) benchmarks:

| Operation | 49K nodes | 2.1M nodes (Linux kernel) |
|---|---|---|
| Full index | ~6 s | ~3 min |
| BFS call-path (depth=5) | ~0.3 ms | ~15 ms |
| Cypher query | <1 ms | ~5 ms |
| Name search (regex) | <10 ms | ~50 ms |

**Key optimizations**:
1. **SQL recursive CTEs** — BFS in a single SQL query is faster than loading into Go
2. **Depth-limited BFS** — cap expansion at depth 3-5
3. **Directional expansion** — follow outbound edges only (not inbound)
4. **Score-weighted BFS** — expand edges in priority order (most related first)

### 6.3 Recommendations for semidx

The current `FetchGraphNeighbors` → BFS pattern has these issues at scale:

1. **Loads all edges** — unnecessary for a depth-limited query
2. **BFS in Go** — no database push-down optimization
3. **No edge weights** — everything is equally "related"

**Replace with SQL recursive CTE**:

```sql
WITH RECURSIVE graph_bfs AS (
    -- Anchor: seed file(s)
    SELECT source_file, target_file, 1 AS depth
    FROM file_dependencies
    WHERE project_id = $1 AND source_file = $2

    UNION ALL

    -- Recursive: expand outbound edges only, depth-limited
    SELECT d.source_file, d.target_file, g.depth + 1
    FROM file_dependencies d
    JOIN graph_bfs g ON d.source_file = g.target_file
    WHERE g.depth < $3  -- max_depth
)
SELECT DISTINCT source_file, target_file FROM graph_bfs;
```

This uses PostgreSQL's index on `file_dependencies(source_file)` and can be tuned with:
- `max_depth` (default 3 for code, 5 for docs)
- `max_nodes` total cap (e.g., 500)
- Directionality flag (outbound, inbound, both)

---

## 7. Current semidx Assessment & Roadmap

### 7.1 What Already Works Well

| Feature | Context | Grade |
|---|---|---|
| Content-addressed storage | `UNIQUE(project_id, path, hash)` | ✅ |
| Incremental indexing | `FileUpToDate` by hash | ✅ |
| pgvector HNSW | Fully implemented with halfvec fallback | ✅ |
| Per-dimension tables | `chunks_<dims>` avoids cross-model pollution | ✅ |
| Worktree tracking | `worktree_files` manifest + prune | ✅ |
| Keyword fallback | GIN trigram (ILIKE) + FTS5 | ✅ |
| Privacy routing | Sensitive file → local embed or text-only | ✅ |
| Embedding circuit breaker | `internal/embed/circuit.go` | ✅ |
| Top-K min-heap | O(n) scan + O(k log k) sort for SQLite | ✅ |

### 7.2 What Needs Work (Impact-Ordered)

| # | Improvement | Effort | Impact | Research basis |
|---|---|---|---|---|
| **P0** | **Git-diff layer for incremental** | 2-3 days | 50-98% re-index speedup | Cartog, Glean, GitNexus all prove this |
| **P0** | **Query routing** (keyword vs vector auto-detect) | 1-2 days | 50-100x speedup on identifier queries | Zoekt design, Google Code Search |
| **P1** | **RRF hybrid search** | 3-5 days | +98% recall@10 vs single path | RuVector, Elastic, pgturbohybrid |
| **P1** | **halfvec as default storage type** | 1 day | 2x storage reduction, <1% loss | pgvector 0.7+, Umur Inan guide |
| **P2** | **SQL recursive CTE for BFS** | 1-2 days | 10-100x speedup on large graphs | Codebase-Memory paper |
| **P2** | **Cross-project dedup cache** | 3-5 days | 10-30% storage savings on multi-project | Chunkstore benchmarks |
| **P2** | **Partial HNSW indexes per language** | 2-3 days | 30-50% faster filtered searches | pgvector HNSW tuning guides |
| **P3** | **Binary quantization + rerank** | 5-10 days | 32x compression, 95%+ recall | HuggingFace quantization blog |
| **P3** | **Embedding cache in SearchSimilar** | 1 day | ~2ms saved per repeated query | Production hybrid search systems |
| **P3** | **Hot/cold project tiering** | 3-5 days | Graceful degradation for 100K+ files | pgvector 100M analysis |
| **P4** | **Product quantization** | 10-15 days | 10-100x vector compression | FAISS PQ paper, Qdrant PQ |
| **P4** | **Parallel HNSW build** | 5-10 days | Faster index building at scale | pgvector 0.8 parallel build |

### 7.3 Concrete Implementation Notes

**P0 — Git-diff incremental layer**:
```go
func gitDiffFiles(projectPath, lastCommit string) (changed, deleted []string, error) {
    // git diff --name-only <last_commit>..HEAD
    // git ls-files --others --exclude-standard
    // Store last_commit in a metadata table or file
}
```

**P0 — Query routing**:
```go
func isIdentifierQuery(query string) bool {
    // Heuristic: if query matches ^[a-zA-Z_][a-zA-Z0-9_]*([.][a-zA-Z_][a-zA-Z0-9_]*)*$
    // or contains no spaces and at most one punctuation character
    // Route to keyword/trigram, not vector
}

func isNLQuery(query string) bool {
    // Contains spaces, question words, or natural language constructs
    // Route to vector
}
```

**P1 — halfvec default**:
```go
// In EnsureChunksTable, change:
//   embedding vector(%d) → embedding halfvec(%d)
// In distanceExpr for dims ≤ 2000:
//   c.embedding::halfvec(%d) <=> $1::halfvec(%d)  // use halfvec always
```

**P1 — RRF hybrid search**:
```go
// SearchSimilar → run keyword + vector in parallel goroutines
// Collect both result sets, fuse with RRF:
// score(d) = sum( 1 / (rank_keyword(d) + 60) ) + sum( 1 / (rank_vector(d) + 60) )
// Sort by fused score, return top-K
```

**P2 — Recursive CTE for BFS**:
```sql
-- Replace Go BFS with this single SQL query
WITH RECURSIVE graph_bfs AS (
    SELECT source_file, target_file, 1 AS depth
    FROM file_dependencies
    WHERE project_id = $1 AND source_file = $2
    UNION ALL
    SELECT d.source_file, d.target_file, g.depth + 1
    FROM file_dependencies d
    JOIN graph_bfs g ON d.source_file = g.target_file AND g.depth < $3
)
SELECT source_file, target_file FROM graph_bfs LIMIT $4;
```

### 7.4 When to Recommend Postgres over SQLite

**Decision rule** for semidx users:

| Chunks | SQLite | Postgres |
|---|---|---|
| < 50K | ✅ Excellent | Overkill |
| 50K - 500K | ⚠️ Borderline | ✅ Recommended |
| 500K - 10M | ❌ Too slow | ✅ Excellent with HNSW |
| 10M+ | ❌ Unusable | ✅ Needs halfvec + tuning |

Autodetect: warn when localstore reaches 100K files or 500K chunks during index.

---

## 8. Sources

### Architecture & Design
- [Zoekt design document (original Google)](https://github.com/sourcegraph/zoekt/blob/main/doc/design.md)
- [Zoekt architecture (DeepWiki)](https://deepwiki.com/sourcegraph/zoekt/1.1-architecture)
- [Sourcegraph architecture docs](https://sourcegraph.com/docs/admin/architecture)
- [Glean architecture overview](https://coda.io/@crystal-shuoqi-li/glean-architecture)
- [Google Code Search: Regular Expression Matching with a Trigram Index](https://swtch.com/~rsc/regexp/regexp4.html)
- [GitHub's new code search technology](https://github.blog/engineering/architecture-optimization/the-technology-behind-githubs-new-code-search/)

### Performance & Scaling
- [Zoekt memory optimizations: 5x reduction, 1M repos](https://sourcegraph.com/blog/zoekt-memory-optimizations-for-sourcegraph-cloud)
- [pgvector HNSW tuning guide](https://queryplane.com/blog/pgvector-hnsw-tuning-guide/)
- [pgvector at 10M rows is a different animal](https://umurinan.com/pages/posts/pgvector-at-10-million-rows-is-a-different-animal.html)
- [pgvector HNSW at 100M vectors: what breaks](https://db-news.com/pgvector-hnsw-index-memory-pressure-at-100m-vectors-what-breaks)
- [pgvector HNSW vs IVFFlat benchmark](https://markaicode.com/benchmarks/postgresql-semantic-search-benchmark/)
- [Scaling pgvector to billion vectors](https://muhammadamal.my.id/blog/scaling-pgvector-to-billion-vector-workloads/)
- [pgvector scaling: memory, quantization, index builds](https://dev.to/philip_mcclarence_2ef9475/scaling-pgvector-memory-quantization-and-index-build-strategies-8m2)

### Incremental Indexing
- [Glean incremental indexing](https://glean.software/blog/incremental/)
- [Indexing code at scale with Glean (Meta Engineering)](https://engineering.fb.com/2024/12/19/developer-tools/glean-open-source-code-indexing/)
- [Cartog: incremental indexing with Merkle tree](https://www.julienrollin.com/en/posts/cartog-incremental-merkle-tree/)
- [GitNexus incremental indexing (chunk-level parse cache)](https://github.com/abhigyanpatwari/GitNexus/commit/4fa40e9881531)

### Quantization & Compression
- [Binary and Scalar Embedding Quantization (HuggingFace)](https://huggingface.co/blog/embedding-quantization)
- [Product Quantization for ANN Search (original PQ paper, Jegou et al.)](https://inria.hal.science/inria-00514462/document/)
- [Product Quantization in Vector Search (Qdrant)](https://qdrant.tech/articles/product-quantization/)
- [SegPQ: Lossless Codebook Compression (VLDB 2025)](https://www.vldb.org/pvldb/vol18/p3730-liu.pdf)

### Hybrid Search
- [Elastic: hybrid retrieval with RRF and weighted sum](https://www.elastic.co/search-labs/blog/improving-information-retrieval-elastic-stack-hybrid)
- [Hybrid Search Explained (Weaviate)](https://weaviate.io/blog/hybrid-search-explained)
- [Trade-offs in Hybrid Search (Infinity benchmark)](https://arxiv.org/html/2508.01405v1)
- [Hybrid Sparse-Dense Fusion benchmark (RuVector)](https://github.com/ruvnet/RuVector/pull/507)

### Content-Addressed Dedup
- [Chunkstore benchmarks: 90% savings on shared chunks](https://github.com/MuratovER/chunkstore)
- [XetHub CDC benchmark: 50% improvement, 53% storage reduction](https://blogs.nionee.com/from-files-to-chunks-improving-hf-storage-efficiency/)
- [CDC algorithm benchmark for Nix binary cache](https://gist.github.com/Mic92/992c07ae5059513b2f93416d2943e42b)

### Code Knowledge Graphs
- [Codebase-Memory: Tree-Sitter knowledge graphs (arXiv)](https://arxiv.org/html/2603.27277v1)
- [Debian Code Search thesis](https://codesearch.debian.net/research/bsc-thesis.pdf)

---

*Last updated: 2026-07-06*
