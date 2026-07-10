# Design Decisions (ADRs)

This document records the main architectural decisions made during the evolution of this POC, motivated by real incidents that occurred in the homelab.

---

## 1. Provider Abstraction and Fallback Chain

- **Decision**: Create the `Embedder` interface and the `ChainEmbedder` implementation allowing multiple embedding providers to be iterated in order of preference.
- **Why**: 
  - *Incident*: External API keys (or the local Ollama) occasionally fail due to timeout, network outage, or quota exhaustion (rate limits on free tier). Tying the CLI to a single provider caused complete indexing interruption.
- **How**: 
  - Implemented in `embedder.go`. `ChainEmbedder` receives a list of ordered providers. Any network failure or timeout during embedding generation automatically switches to the next in the list (e.g., Gemini -> OpenRouter -> Local Ollama).
- **Trade-offs**: 
  - Different models generate vectors of different dimensions (e.g., bge-m3 1024d vs Gemini 3072d). Semantic search **does not work** if we mix vectors from different models in the same index (produces mathematical garbage). Therefore, the chain can only fallback between endpoints serving the *same* model or models with equivalent dimensions, or the project must be reindexed from scratch if the model is changed.

---

## 2. Dynamic Vector Tables Based on Dimension

- **Decision**: Abandon the single `chunks` table with fixed size `vector(1024)` in favor of dynamic tables such as `chunks_768`, `chunks_1024`, and `chunks_3072`.
- **Why**: 
  - *Incident*: pgvector requires a fixed size in the column declaration (e.g., `vector(1024)`). Lighter models (like `nomic-embed-text` at 768d) or more precise ones (like `gemini-embedding-2` at 3072d) broke the SQL insert with syntax errors and constraint violations.
- **How**: 
  - Implemented in `db.go`. The `EnsureChunksTable(ctx, dims)` method dynamically creates the corresponding table (`chunks_X`) based on the dimensions reported by the model during initialization.
- **Trade-offs**: 
  - Adds complexity to SQL (using string concatenation for table names). However, it keeps the database clean and flexible to accept any model on the market without manual schema migrations.

---

## 3. Indexing Sandboxing via Docker with RAM Capping (512MB)

- **Decision**: Run heavy indexing strictly inside Docker containers with hard memory limits (`--memory 512m`).
- **Why**: 
  - *Incident*: The conventional `opencode` indexing process (Bun-based) and early runs of the Go POC inflated the homelab RAM from 20GB to 23GB, exhausting the 4GB swap and causing total system freeze (thrashing) of the 31GB physical machine before being killed by the OOM-killer.
- **How**: 
  - Created the script `/home/lgldsilva/poc-semantic-indexer/index-project.sh`. It compiles the application statically (`CGO_ENABLED=0`) and runs it in a lightweight `alpine` container with cgroup limits applied by Docker's runtime.
- **Trade-offs**: 
  - Requires Docker to be installed and running on the host. However, it protects the physical machine against any memory leaks or processing spikes, making execution 100% safe.

---

## 4. Routing and Protection of Sensitive Files

- **Decision**: Detect files containing secrets or confidential information (`.env`, `auth`, `secret`, `key`) and force embedding generation strictly **locally** (via Ollama). If the project is using a cloud model (like Gemini), the sensitive file is indexed only as **local plain text** (without sending to the cloud).
- **Why**: 
  - *Incident*: Sending configuration files, database credentials, or private keys to cloud APIs (Google Gemini/OpenRouter) violates basic security rules and exposes homelab secrets.
- **How**: 
  - Implemented in `chunker.go` (`IsSensitive`) and `indexer.go`. The indexer uses the `WithForceLocal` flag via Go context. If the local model generation fails or is not supported (e.g., Gemini in the cloud), the indexer saves the text chunk with the `embedding` vector set to `NULL`.
- **Trade-offs**: 
  - Sensitive chunks indexed without embedding do not appear in pure semantic search. However, they remain discoverable by the **Keyword Search Fallback (FTS)** mechanism, ensuring security without losing searchability.

---

## 5. Keyword Fallback (SQL ILIKE) and Auto-Detection of Table

- **Decision**: Implement classic text search with automatic database table scanning support if the embedding API is completely offline.
- **Why**: 
  - *Incident*: If the local Ollama is shut down and the internet is down, semantic search breaks because it cannot convert the query into a vector.
- **How**: 
  - Implemented in `db.go` (`SearchSimilarKeywords`). The method splits the query into words and runs combined searches using `ILIKE` in the database. If we don't know the model or the dimension, the Go POC queries the system table `pg_tables` to find which `chunks_*` table has records written for that project.
- **Trade-offs**: 
  - Text search does not understand synonyms (e.g., searching for "user" in Portuguese won't find "usuario"), but it keeps search operational and resilient under any infrastructure failure circumstances.

---

## 6. ANN Index (HNSW/halfvec) and Line Numbers in the Database

- **Decision**: Create an **HNSW** cosine index on each `chunks_<dims>` table and persist `start_line`/`end_line` for each chunk in the database (reverting the original decision to calculate the line by reading the file at search time).
- **Why**:
  - Without an ANN index, vector search performed *sequential scan* — unacceptable as projects grow.
  - The line calculation by reading the file (`findLineInFile`) requires the file to be accessible on the search host — impossible in the client-server architecture (the server does not have the files) — and the algorithm found the *first* occurrence of the line anywhere in the file (fragile).
- **How**:
  - `EnsureChunksTable` creates `CREATE INDEX ... USING hnsw`. **pgvector gotcha**: HNSW on the `vector` type is limited to **2000 dimensions**; larger models (Gemini 3072) index using the `halfvec` cast (`(embedding::halfvec(N)) halfvec_cosine_ops`), and `SearchSimilar` queries the equivalent expression so the index is used.
  - `Chunk` carries `StartLine`/`EndLine` (computed in the `chunker`), persisted in `chunks_<dims>` and returned in `SearchResult`. `GrepFormatter` uses the line from the database; `findLineInFile` was removed.
- **Trade-offs**:
  - `halfvec` reduces index precision (half precision) for models >2000d — acceptable for approximate recall; exact distance still uses `vector`. Old tables receive the columns via `ALTER TABLE ADD COLUMN IF NOT EXISTS` (upgrade without `drop`), but pre-migration data stays with null line data (reindex to populate).

---

## 7. Global Embedding De-duplication (Relational Dictionary)

- **Context**: The same file across worktrees/subprojects (monorepos, shared
  libraries) embeds the same text repeatedly. The **`embedding_cache_<dims>`**
  table (Postgres) / **`embedding_cache`** table (SQLite), keyed by
  `(input_hash, model)`, already **skips the redundant embedding API call** on a
  hit — so RF04 (bypass) and RF01/RF02 (dictionary keyed by content-hash+model)
  are effectively met. What remains (RF03, storage efficiency) is that
  `chunks_<dims>` still stores the **embedding vector redundantly** per project.
- **Decision**: `chunks_<dims>` stops storing the `embedding` column and instead
  references the dictionary by `input_hash`. Vector search joins
  `chunks → embedding_cache` on `(input_hash, model)` to fetch the vector.
- **Key subtlety** (`input_hash` ≠ `hash(content)`): the dictionary is keyed by
  the hash of the **embedder input** (symbol-enriched, see the indexer), not the
  raw chunk content. So the chunk must carry the **embed-input hash**, and the
  raw `content` **stays in `chunks_<dims>`** (needed for display and for the
  keyword/FTS path — moving it would force an FTS5/trigram rework for marginal
  gain). Only the vector is de-duplicated.
- **SQLite** (validated without Docker): brute-force cosine already scans the
  project's chunks in Go, so it just joins to the dictionary for the vector.
  No ANN, no recall concern — implemented and unit-tested first as the reference.
- **Postgres — BENCHMARKED, decision: keep the per-project `embedding` (option
  c).** Moving the vector to the shared dictionary would put the HNSW ANN index
  on the dictionary, forcing per-project search into one of two losing shapes.
  `TestDedupRecallBenchmark` (`internal/store/dedup_bench_test.go`, pgvector
  testcontainer, 16 projects × ~240 chunks, dims 256, `hnsw.ef_search=100`)
  measured, against Go-computed exact ground truth:
  | search | recall@10 | p50 |
  |---|---|---|
  | baseline (inline vector, ANN + project filter) | **1.000** | 0.47 ms |
  | dedup global-ANN over-fetch ×1 / ×4 / ×8 / ×16 | 0.099 / 0.398 / 0.769 / 0.993 | ~2 ms |
  | dedup project-first (exact rank, no dict ANN) | 1.000 | 1.11 ms (buffers 725 vs 38) |
  - **Option (b) global-ANN + over-fetch**: severe recall regression — needs
    ~16× over-fetch just to match baseline, and the required factor grows with
    the project count (the global top-K is dominated by *other* projects'
    vectors), so it does not scale to a multi-project server.
  - **Option (a)/project-first exact**: recall-correct but abandons the ANN — the
    `EXPLAIN` shows a per-row dictionary lookup (240 loops, 19× the buffer I/O)
    and, crucially, **cannot use the dictionary HNSW**, degenerating to a
    sequential scan for large projects — the exact failure the HNSW was added to
    fix (§6).
  - Baseline lets Postgres pick the optimal plan per project size (exact filter
    for small projects, HNSW for large) at recall 1.000 / ~0.5 ms. The storage
    saving (1.66× here) does not justify breaking that. **RF04 (skip the
    redundant embedding *API call*) and RF01/RF02 (dictionary keyed by
    content-hash + model) are already met on Postgres by `embedding_cache_<dims>`**
    — only redundant vector *storage* remains, an acceptable trade. The storage
    win therefore lands on SQLite (brute-force scan, no ANN, no recall concern),
    shipped here; Postgres keeps its per-project `embedding` unchanged.
  - Reproduce: `SEMIDX_BENCH_DEDUP=1 go test ./internal/store -run
    TestDedupRecallBenchmark -v` (skipped in normal `go test ./...`).
- **GC (RF05 / RNF02)** on SQLite: a dictionary row is orphaned when no chunk
  references its `emb_hash`; `PruneOrphanEmbeddings` sweeps them. Project/file
  delete cascades chunk rows only — never the dictionary (RNF02).

---

## 🚫 What we will NOT do (for now)

- **`models` table in the database**: `InferDims` (name→dimension map) is already the single source in `internal/embed`; moving it to a database table would couple `embed`→`store` for marginal benefit. Re-evaluate if/when per-model config (provider/local) without recompilation is needed.
- **Indexing Large Files (>1MB)**: Giant files are ignored or truncated. This project is optimized for source code and structured markdown documentation.

---

## ⚠️ Error Checklist to Avoid

- [ ] **Don't use `os.ReadFile` directly**: It allocates the entire file in memory before splitting. Always use `os.Open` + `io.LimitReader` for files of uncertain size.
- [ ] **Don't use `\r` in background logs**: Carriage return characters break the display buffer of agent TUIs, causing slowness and rendering freezes. Use line-based structured logs with `\n`.
- [ ] **Don't mix models in the same project**: Keep the same model (e.g., `bge-m3`) across all index operations for a project, or call `drop` before switching.
