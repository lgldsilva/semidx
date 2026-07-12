# Requirements — semidx

Product requirements for **semidx**: self-hosted semantic code (and document)
search, with a single Go binary acting as CLI, HTTP server, MCP client surface,
and web admin.

This document is the **forward-looking contract**: what the product must do,
what is already satisfied, and what remains. It complements:

| Doc | Role |
|-----|------|
| [architecture.md](./architecture.md) | How it is built |
| [api.md](./api.md) · [openapi.yaml](./openapi.yaml) | Public HTTP contract |
| [self-hosting.md](./self-hosting.md) · [CICD.md](./CICD.md) | Ops and release |
| [design-decisions.md](./design-decisions.md) | ADRs (why) |
| [ANALISE-PRODUTO.md](./ANALISE-PRODUTO.md) | Historical audit findings (2026-07-03) |
| [auditoria-2026-07-07.md](./auditoria-2026-07-07.md) | Snapshot audit vs roadmap |

**Status legend**

| Status | Meaning |
|--------|---------|
| **done** | Implemented and usable in the current tree |
| **partial** | Exists on some surfaces or with known gaps |
| **todo** | Not delivered; accepted as a requirement |
| **wont** | Explicitly out of scope (see §8) |

IDs are stable (`REQ-*`). Prefer adding rows over renumbering.

---

## 1. Product principles

### 1.1 What semidx is

1. **Self-hosted semantic search** for code and documents: chunk → embed → store
   → ranked `file:line` results for natural-language queries.
2. **One binary, several faces**: CLI (primary), `serve` (HTTP API + `/admin`
   SPA), MCP (thin agent client).
3. **Client–server and standalone**: remote server (Postgres/pgvector), local
   SQLite, or CLI proxy after `semidx login`.
4. **Privacy-aware embeddings**: sensitive paths/content prefer local embed or
   keyword-only storage; cloud keys stay on the machine that embeds.
5. **Agent-friendly**: MCP tools + install helpers + skills; not a SaaS and not
   a grep replacement.
6. **Workspace agent**: in addition to search, semidx provides a conversational
   agent (chat + tools) that answers questions about both **content** and
   **state** of repositories, documents, and projects — git metadata
   (worktrees, branches), index status, and semantic Content. Dual CLI/server:
   the CLI sees the local filesystem and git; the server manages shared repo
   clones. The agent loop (tool calling via LLM) activates when a chat model
   with tool support is configured; without one, RAG-only is the fallback.

### 1.2 What semidx is not

- Not a hosted multi-tenant SaaS or billing product.
- Not a full IDE, debugger, or CI quality platform (e.g. PR Sonar analysis).
- Not a substitute for exact-string tools (`rg`/`grep`).
- Not a general archive browser (arbitrary `.zip`/`.tar` content) unless listed
  under extractors below.

---

## 2. Personas and surfaces

| Persona | Primary surface | Goal |
|---------|-----------------|------|
| Developer (solo / laptop) | CLI + optional local SQLite | Index repo, search by intent, zero server |
| Team / homelab admin | `semidx serve` + `/admin` | Shared index, users, jobs, git-sync |
| AI agent | MCP (`semantic_search`, …) | Find code by meaning inside an agent session |
| Integrator | HTTP `/api/v1/*` + `pkg/client` | Automate index/search from other tools |

**Parity rule (REQ-PARITY-01):** For every user-visible capability, document
which of **CLI · API · Admin UI · MCP** implement it. Gaps are either planned
(`todo`/`partial`) or explicit `wont`.

---

## 3. Functional requirements

### 3.1 Indexing and content

| ID | Requirement | Status | Notes |
|----|-------------|--------|-------|
| REQ-IDX-01 | Index git repositories with stable **repo identity**; content-addressed chunks so the same file hash embeds once | **done** | Worktree filter on search |
| REQ-IDX-02 | Index plain document trees (`--docs`) keyed by absolute path | **done** | |
| REQ-IDX-03 | Index archive formats used as code packages (JAR/WAR/AAR) including `.class` (optional decompiler) | **done** | `SEMIDX_JAVA_DECOMPILER` |
| REQ-IDX-04 | Pluggable extractors for code/text, Markdown/HTML, PDF, OOXML, OpenDocument, EPUB, Jupyter, RTF | **done** | Registry in `internal/extract` |
| REQ-IDX-05 | Hash-diff incremental index; reindex only changed content | **done** | |
| REQ-IDX-06 | Configurable max file size / max chunks per file / embed batch size | **done** | `SEMIDX_MAX_*` + `SEMIDX_EMBED_BATCH_SIZE` |
| REQ-IDX-07 | Bulk insert worktree manifests (no per-row round-trip at large scale) | **done** | `CopyFrom` in `SetWorktreeFiles` |
| REQ-IDX-08 | Public extractor registration API for custom extensions | **done** | `internal/extract.Register` |
| REQ-IDX-09 | Extract legacy Office (`.doc/.xls/.ppt`), email, images/OCR, generic zip/tar | **wont** (near-term) | Revisit if a real user case appears |
| REQ-IDX-10 | Progress reporting during long index (CLI + jobs) | **done** | Local index verbose progress + async push and `repo add --wait` live status/%/counters |
| REQ-IDX-11 | Watcher re-indexes document extracts (PDF/DOCX/…), not only `ShouldIndex` code/text files | **done** | Fix in `internal/indexing/watcher.go` + shared `Eligible` helper; ensures same predicate as the walk indexer |

### 3.2 Embedding and privacy

| ID | Requirement | Status | Notes |
|----|-------------|--------|-------|
| REQ-EMB-01 | Provider chain with ordered fallback (cloud → local Ollama) | **done** | Gemini → Groq → OpenRouter → Ollama Cloud → local |
| REQ-EMB-02 | Keyword-only mode with no embedding model (`--keyword` / `SEMIDX_EMBED_MODE=none`) | **done** | |
| REQ-EMB-03 | Zero-config path: when nothing is configured, usable keyword+local behaviour with a clear warning | **done** | Zero-config auto-enables local keyword mode with warning and CLI fallback guidance |
| REQ-EMB-04 | Sensitive files never sent to cloud embedders; local embed or text-only | **done** | Privacy engine |
| REQ-EMB-05 | Reduce false-positive “sensitive” classification (e.g. path segment `key` matching `keyboard.go`) | **done** | Segment-aware matching + tests |
| REQ-EMB-06 | Embedding HTTP clients use connection pooling and per-provider timeouts | **done** | OpenAI/Ollama clients use pooled transports + timeouts |
| REQ-EMB-07 | Content-addressed embedding cache reused across reindexes; no silent gaps on git-history / pre-embedded push paths | **done** | Cache reuse covers reindex/git-history/pre-embedded push |
| REQ-EMB-08 | Dynamic vector tables by dimension; refuse silent mix of incompatible models in one project | **done** | ADR-style decision |

### 3.3 Search and ranking

| ID | Requirement | Status | Notes |
|----|-------------|--------|-------|
| REQ-SRCH-01 | Semantic vector search with ranked results including `file`, `start_line`, `end_line`, score | **done** | |
| REQ-SRCH-02 | Explicit keyword fallback when embeddings unavailable (`fallback: true`) | **done** | |
| REQ-SRCH-03 | Hybrid search as default (vector + lexical/FTS + fusion, e.g. RRF) when both available | **done** | Service now runs hybrid by default |
| REQ-SRCH-04 | Graph-RAG expansion (import/dependency graph, bounded depth) | **done** / **partial** | CLI/API/MCP; depth hard-cap still required for abuse |
| REQ-SRCH-05 | Hard upper bound on `graph_depth` and BFS cost | **done** | Clamp + max expanded paths |
| REQ-SRCH-06 | Project resolution: path, name, CWD enclosing project, or all projects with labels | **done** | |
| REQ-SRCH-07 | Worktree filter for git projects; ignored for docs/push projects | **done** | Gotcha documented in AGENTS.md |
| REQ-SRCH-08 | User-safe error messages (no raw DB/provider strings to clients) | **done** | HTTP search API, admin job/ingest, admin all-projects search, and the standalone MCP backend all sanitize raw internal errors; CLI-local keeps raw errors on stderr (operator on their own index, no trust boundary) |
| REQ-SRCH-09 | Human and grep-friendly formatters with `file:line` and understandable scores | **done** | Human formatter shows explicit raw+percent score; sgrep `file:line:content` contract preserved |
| REQ-SRCH-10 | Relevance benchmark suite (gold queries + scoring), not only latency benches | **done** | `semidx bench` + `docs/bench-queries.json` |
| REQ-SRCH-11 | Optional re-ranker (e.g. local cross-encoder) on top-K | **done** | Pluggable `search.Reranker` seam + dependency-free `LexicalReranker` (blends score with query-term overlap); off unless `SEMIDX_RERANK_WEIGHT` ∈ (0,1]. A cross-encoder can implement the same interface later |
| REQ-SRCH-12 | Language-aware / AST-aware chunking for major languages | **partial** | AST/symbol-aware chunking when analyzer symbols are available |

### 3.4 Storage backends

| ID | Requirement | Status | Notes |
|----|-------------|--------|-------|
| REQ-STOR-01 | Three backends: remote HTTP, Postgres/pgvector, local SQLite | **done** | Precedence: remote > Postgres configured > SQLite > Postgres default |
| REQ-STOR-02 | `semidx config list` reports active backend | **done** | |
| REQ-STOR-03 | SQLite safe concurrency (WAL, busy timeout, single writer conn) | **done** | |
| REQ-STOR-04 | SQLite search does not load all vectors into memory for large indexes (fixed top-K heap) | **done** | Streaming top-K heap in local store |
| REQ-STOR-05 | Keyword path uses proper full-text (Postgres `pg_trgm`/FTS, SQLite FTS5), not unbounded leading-wildcard scans | **done** | pg_trgm migration + SQLite FTS5 |
| REQ-STOR-06 | Pagination for list endpoints (projects, users, jobs, files) | **done** | API/admin list endpoints now expose limit/offset across projects/users/jobs/files |
| REQ-STOR-07 | Migrate SQLite → Postgres (`semidx migrate`) | **done** | |

### 3.5 Server, auth, and jobs

| ID | Requirement | Status | Notes |
|----|-------------|--------|-------|
| REQ-SRV-01 | HTTP API under `/api/v1/*` with bearer tokens and scopes | **done** | |
| REQ-SRV-02 | Web admin at `/admin` (SPA) with session + CSRF | **done** | Embedded `internal/webui/dist` |
| REQ-SRV-03 | Users, API keys, JWT control tokens, projects management | **done** | argon2id, server sessions |
| REQ-SRV-04 | Server-side git clone/pull and reindex jobs | **done** | `repo add`, job workers |
| REQ-SRV-05 | Push raw files for server-side chunk+embed; optional client embed-local | **done** | `semidx push` |
| REQ-SRV-06 | HTTP timeouts, body size limits, rate limiting on API | **done** | See server package |
| REQ-SRV-07 | Bootstrap admin credential handling without logging secrets by default | **done** | Bootstrap token persisted to file by default; explicit `--show-bootstrap-token` opt-in for stderr display |
| REQ-SRV-08 | Persistent CSRF key across restarts / multi-instance | **done** | `SEMIDX_CSRF_KEY` wired in config/server |
| REQ-SRV-09 | Job isolation: users cannot read/control other projects’ jobs by ID (no IDOR) | **done** | Project-scoped job endpoints + validation |
| REQ-SRV-10 | Job progress visible in admin (live status, files/chunks, errors) | **done** | Polling + persisted counters and `%` |
| REQ-SRV-11 | Job workers react without long empty-queue poll (e.g. LISTEN/NOTIFY) | **done** | `JobNotifier` + LISTEN/NOTIFY fallback |
| REQ-SRV-12 | Configurable per-project resource caps (max chunks/files) | **todo** | |
| REQ-SRV-13 | Postgres never published; only authenticated HTTP ingress | **done** | Compose design |
| REQ-SRV-14 | Server can also act as a client (login/push/index remote) — mixed mode | **done** | Mixed backend mode (`--local`/`--backend`) with remote login/push/repo flows is operational |

### 3.6 Admin UI (SPA)

| ID | Requirement | Status | Notes |
|----|-------------|--------|-------|
| REQ-UI-01 | Login, system banner, projects list with filters/sort | **done** | |
| REQ-UI-02 | Project workspace: overview, files, explore, chat | **done** | |
| REQ-UI-03 | Search playground | **done** | |
| REQ-UI-04 | Settings and jobs views | **done** / **partial** | |
| REQ-UI-05 | Browser ingest (upload), explain, graph-oriented explore | **partial** | Recent work; bulk folder still open |
| REQ-UI-06 | Table actions stay inside the grid (Open + overflow menu) | **partial** | UX polish in progress |
| REQ-UI-07 | Bulk folder / archive upload for push projects | **done** | Folder batching + `.zip` ingest in Admin UI/API |
| REQ-UI-08 | File detail panel: chunks, dependency fan-out, caller fan-in, deep links | **partial** | Chunks + fan-in/fan-out delivered; deeper graph UX pending |
| REQ-UI-09 | Graph visualization (or progressive disclosure of graph stats) | **done** | Graph overview in Analyze: node/edge counts + top out-/in-degree nodes (`/graph-stats`); CSP-safe, no external viz lib |
| REQ-UI-10 | Dead-code, SBOM, secrets, diff, alerts, insights surfaces where CLI has them | **todo** | Or link out to CLI guide with status |
| REQ-UI-11 | First-run / empty-state guidance (no projects → how to add) | **done** | Projects empty-state onboarding flow |

### 3.7 CLI

| ID | Requirement | Status | Notes |
|----|-------------|--------|-------|
| REQ-CLI-01 | Core: `index`, `search`, `sgrep`, `serve`, `mcp`, `config`, `login`, `push`, `repo`, `drop`, `migrate` | **done** | |
| REQ-CLI-02 | Analysis: `callers`, `explain`, `dead-code`, `diff`, `sbom`, secrets-related flows | **done** / **partial** | |
| REQ-CLI-03 | Productivity: `alerts`, `insights`, `init`, `upgrade`, profiles | **partial** | Often local JSON; not always server-backed |
| REQ-CLI-04 | Destructive commands require confirmation (`--confirm` / prompt) | **partial** | `drop` and `alerts delete` now require explicit confirmation |
| REQ-CLI-05 | Help grouped by workflow with `Long` + real examples | **partial** | Added richer `Long`/examples for alerts and insights subcommands |
| REQ-CLI-06 | User-facing messages in English; no mixed PT/EN in errors | **done** | CLI package now has regression test enforcing English-only user-facing strings |
| REQ-CLI-07 | Backend mode flags: `--local` / `--backend` / `--profile` behave consistently | **done** | Backend/profile precedence implemented and covered by CLI tests |

### 3.8 MCP and agents

| ID | Requirement | Status | Notes |
|----|-------------|--------|-------|
| REQ-MCP-01 | Stdio MCP tools: search, projects, reindex (server mode) | **done** | Logs on stderr only |
| REQ-MCP-02 | Standalone MCP over local index when no server | **done** | Reindex returns guidance to CLI |
| REQ-MCP-03 | `semidx mcp install` for major agent clients | **done** | 12 clients; some print-only |
| REQ-MCP-04 | Bundled skills install | **done** | |
| REQ-MCP-05 | Integration harness in CI (at least keyword mode) | **done** | CI now runs `deploy/agentics-test/run.sh keyword` as a gate |

### 3.9 Operations and observability

| ID | Requirement | Status | Notes |
|----|-------------|--------|-------|
| REQ-OPS-01 | Health endpoints (`/healthz`, `/readyz`) | **done** | |
| REQ-OPS-02 | Prometheus metrics for request volume | **done** | |
| REQ-OPS-03 | Latency histograms (search, embed) and gauges (queue, DB pool) | **done** | Search + embed (`semidx_embed_duration_seconds`, `semidx_embed_inputs_total` by model/outcome) histograms; job queue/running + DB pool gauges |
| REQ-OPS-04 | Structured logging (`slog`) across indexer, search, embed chain | **partial** | |
| REQ-OPS-05 | Pull-based deploy (Watchtower); CI never SSHes into hosts | **done** | Homelab model |
| REQ-OPS-06 | CI gates: build, race tests, lint, gitleaks, govulncheck, gosec, image scan | **done** | |
| REQ-OPS-07 | CI builds SPA and embeds dist (no hand-committed stale assets as sole path) | **done** | CI test job builds SPA before Go gates |
| REQ-OPS-08 | CI runs on `main` pushes (Sonar Community main-only gate) | **todo** | Known Gitea trigger issue |
| REQ-OPS-09 | Reliable integration tests without container-per-test flakes | **partial** | Shared pgvector container recommended |

### 3.10 Workspace agent — conversational + tools

| ID | Requirement | Status | Notes |
|----|-------------|--------|-------|
| REQ-AGENT-01 | Conversational agent with tool-calling loop (chat/MCP). LLM decides whether to call git tools, search, or index tools; supports multi-round tool invocation with max-rounds guard | **todo** | `internal/agent` loop; activates only when LLM with tool calling is configured |
| REQ-AGENT-02 | Tool calling in `internal/chat` (Gemini + OpenAI-compatible function definitions). `Request.Tools`, `Response.ToolCalls`, `StreamChunk.ToolCalls` | **todo** | Schema translation per provider; aggregation across streaming chunks |
| REQ-AGENT-03 | Scope resolver: project ref → (path, identity, source). Injected once into agent, tools call it — no per-tool `projectref` duplication | **todo** | `agent.ScopeResolver` interface |
| REQ-AGENT-04 | Query routing (`ClassifyQuery`) wired into `search.Service.Search`. Heuristic identifier/path/exact/NL → adjust recall weights | **todo** | Already implemented (`routing.go`) but dead code |
| REQ-AGENT-05 | Graph expansion in the agent/chat retrieval path (not only `--graph` flag). Expand search results via dependency import BFS when query suggests code intent | **todo** | `rag.Pipeline` passes `Graph:true`; admin/chat adapters propagate it |
| REQ-AGENT-06 | Multi-scope fused search (`SearchMulti`): across projects + optional path prefix. Cross-project RRF ranking with provenance labels + per-source diversification | **todo** | `*search.Service.SearchMulti`; provenance in `MultiResponse` envelope, not in `store.SearchResult` |
| REQ-GIT-01 | Tools `repo_worktrees`, `repo_branches`, `repo_status` (git read-only). Hermetic, secured, locale-stable (`git for-each-ref`, `git worktree list --porcelain`) | **todo** | `internal/repotools` package; `internal/gitexec` shared git runner |
| REQ-GIT-02 | Capability gating: local CLI exposes git tools + local index tools; remote/server exposes index/reindex tools only (no client worktrees) | **todo** | `agent.Capabilities` bitmask + capability-based MCP tool registration |
| REQ-MCP-06 | MCP tools repotools (local) + semantic_search multi-scope + semantic_ask agentic (gated on CapToolCalling). Remote MCP omits git tools, returns "unsupported on server" | **todo** | Sub-interface pattern (`GitBackend`, `MultiSearchBackend`) |
| REQ-MCP-07 | MCP action tools (`index_project`, `reindex`) with default policy: **propose** on MCP, **confirm** on CLI REPL. Never ARBITRARY path index on remote | **partial** | `action_policy` enum + propose/confirm/execute; propose implemented, confirm/execute deferred |
| REQ-ACT-01 | Action tools: `index_worktree` (local), `reindex_project`, `server_repo_sync` (if logged in). Policy propose/confirm/execute | **partial** | Tools implemented in `internal/agent/actions.go`; propose-only in Phase 5, confirm/execute deferred |
| REQ-ACT-02 | `semidx repo sync` — trigger pull + reindex on server via jobs API | **partial** | `server_repo_sync` action tool in propose mode; enqueues server job on execute |

---

## 4. Surface parity matrix

Target state for core capabilities. Update this table when closing gaps.

| Capability | CLI | API `/api/v1` | Admin UI | MCP |
|------------|-----|---------------|----------|-----|
| Create/list/delete project | yes | yes | yes | list (projects tool) |
| Git-sync project | yes | yes | yes | — |
| Push files / ingest | yes | yes | partial | — |
| Reindex | yes | yes | yes | server-only |
| Semantic search | yes | yes | yes | yes |
| Keyword / fallback | yes | yes | partial | yes |
| Graph-RAG search | yes | yes | partial | yes |
| File tree / content view | — | partial | yes | — |
| Callers / deps / explain | yes | partial | partial | — |
| Chat RAG | yes (related) | admin BFF | yes | — |
| Jobs list / status | yes | yes | yes | — |
| Users / API tokens | admin | admin | yes | — |
| Alerts / insights | partial | — | — | — |
| Dead-code / SBOM / secrets | yes | — | — | — |
| Git worktrees / branches / status | yes | partial (server checkout) | — | local, gated |
| Search multi-scope (project+path prefix) | yes | — | — | yes (local) |
| Agentic ask (tool-calling) | REPL | — | — | gated on LLM tools |
| Action index / reindex / sync | yes | jobs | — | propose default |
| Agent capability gating | yes | — | — | local vs remote |

**REQ-PARITY-02:** Closing a row means automated or manual checks on each
surface that claims “yes”, not only CLI demos.

---

## 5. Non-functional requirements

| ID | Requirement | Target |
|----|-------------|--------|
| REQ-NFR-01 | Coverage on business packages (`internal/**`, `pkg/**`) | ≥ 90% (Sonar denominator; `cmd`/`deploy` excluded) |
| REQ-NFR-02 | Race-safe tests in CI | `go test -race -shuffle=on ./...` |
| REQ-NFR-03 | Toolchain | Go **1.25.12** pin (`GOTOOLCHAIN`); bump builder image with stdlib |
| REQ-NFR-04 | Security tooling | golangci-lint, gosec, govulncheck, gitleaks, Trivy on image |
| REQ-NFR-05 | License | Apache-2.0 |
| REQ-NFR-06 | Secrets | No keys/DSNs in git; config via env / `semidx config` |
| REQ-NFR-07 | MCP stdio | Protocol on stdout; logs on stderr only |
| REQ-NFR-08 | Commits | Conventional Commits; no direct push to `main` (PR flow) |
| REQ-NFR-09 | Default deploy posture | Authenticated API only; Postgres not exposed |
| REQ-NFR-10 | Latency (homelab, warm) | Interactive search p95 under a few hundred ms for mid-size indexes (measure; not yet enforced SLO) |

---

## 6. Prioritized delivery backlog

Ordered for product leverage (not audit severity alone). Each phase should be
one or more PRs with tests.

### Phase A — Finish the daily product loop

1. REQ-UI-07 bulk folder/archive ingest  
2. REQ-UI-08 file detail (chunks + fan-in/fan-out)  
3. REQ-SRV-10 richer live jobs  
4. REQ-PARITY-01/02 matrix kept accurate; close dead-code/SBOM/insights policy  
5. REQ-UI-06/UX polish (actions, empty states)

### Phase B — Search quality

1. REQ-SRCH-11 optional re-ranker  
2. REQ-EMB-07 cache completeness  
3. REQ-SRCH-12 language-aware chunking (incremental languages)  
4. Relevance dashboarding for `semidx bench` in CI/nightly  
5. Query-intent adaptive routing and scoring policy  

### Phase C — Hardening

1. Chat/RAG and secondary HTTP entrypoints: always auth + project validation  
2. REQ-SRCH-08 user-safe errors end-to-end  
3. Abuse limits and quotas review across read-heavy endpoints  
4. Harden error taxonomy (no infra leakage in user-facing surfaces)  
5. Security regression tests for authz/isolation routes  

### Phase D — Scale

1. REQ-OPS-03 richer metrics  
2. REQ-SRV-12 per-project resource caps  
3. REQ-STOR-06 broader pagination parity  
4. REQ-EMB-07 cache completeness under all ingest modes  
5. Large corpus validation and perf budget checks  

### Phase E — Factory / contributor DX

1. REQ-OPS-08 main CI triggers  
2. REQ-MCP-05 harness gate  
3. OpenAPI as single contract source  
4. Store interface segregation  
5. Contributor docs for parity checks and acceptance flow  

---

## 7. Acceptance criteria (definition of done)

A requirement moves to **done** only when:

1. **Behaviour** matches this doc (happy path + at least one failure path).  
2. **Tests** cover the business package change (`go test`, race where concurrent).  
3. **Surfaces** listed as “yes” in §4 work without undocumented flags.  
4. **Docs** updated if user-facing (`README`, `api.md`, or this file’s status).  
5. **No secret leakage** in logs, API errors, or committed files.

---

## 8. Explicit non-goals (near term)

| ID | Non-goal |
|----|----------|
| REQ-WONT-01 | Multi-tenant SaaS, orgs, billing, cloud-hosted control plane |
| REQ-WONT-02 | Replacing ripgrep for exact string search |
| REQ-WONT-03 | Full PR/branch analysis on Sonar Community (main-only is intentional) |
| REQ-WONT-04 | Bundling Ollama or a GPU runtime |
| REQ-WONT-05 | Push-deploy over SSH from CI |
| REQ-WONT-06 | Complete parity with every language LSP feature |

---

## 9. Traceability

| Source | How it maps here |
|--------|------------------|
| `ANALISE-PRODUTO.md` | Many HIGH/MEDIUM items → REQ-* rows; status may be newer than the audit |
| `auditoria-2026-07-07.md` | Delivery % and security findings → Phase C + residual partials |
| Session roadmap (CLI/UI parity, hybrid search, CI SPA) | Phases A–E |
| `AGENTS.md` | Principles, backends, CI commands — normative for agents |

When implementing, prefer updating **status** in this file over inventing a
second checklist.

---

## 10. Change log

| Date | Change |
|------|--------|
| 2026-07-09 | Status refresh after implementation: hybrid default, graph caps, scoped job APIs/progress, SQLite top-K heap, worktree bulk writes, CI SPA build, and UI onboarding/ingest/detail updates |
| 2026-07-12 | Workspace agent expansion: REQ-IDX-11 (watcher doc fix), REQ-AGENT-* (tool-calling agent, query routing, graph-in-chat, multi-scope fuse), REQ-GIT-* (repo tools, capability gating), REQ-ACT-* (action tools, sync), REQ-MCP-* (extended MCP tools) |
| 2026-07-08 | Initial requirements doc from product principles, parity goals, audits, and post-SPA roadmap |
