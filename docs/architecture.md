# Architecture

semidx is a single Go binary (`cmd/semidx`) that behaves as a CLI, an HTTP
server, or an MCP server depending on the subcommand. This document explains how
those pieces fit together and the decisions behind them.

## Two deployment shapes from one binary

```
        Standalone (one machine)                Client / Server (shared)

  ┌───────────────────────────────┐     ┌───────────────┐      ┌──────────────────────────┐
  │  semidx --local index/search  │     │  semidx CLI   │      │      semidx serve        │
  │                               │     │  semidx mcp   │─────▶│  HTTP API  ·  /admin UI  │
  │   ┌───────────────────────┐   │     │  (thin MCP)   │ HTTP │   ┌──────────────────┐   │
  │   │   SQLite index.db     │   │     │  pkg/client   │      │   │ search · jobs ·  │   │
  │   │   (brute-force cosine)│   │     └───────────────┘      │   │ files · auth     │   │
  │   └───────────────────────┘   │            Bearer token   │   └────────┬─────────┘   │
  └───────────────────────────────┘                           │            │             │
                                                               │   ┌────────▼─────────┐   │
                                                               │   │ PostgreSQL /     │   │
                                                               │   │ pgvector (HNSW)  │   │
                                                               │   └──────────────────┘   │
                                                               └──────────────────────────┘
```

- **Standalone** (`--local` or `SEMIDX_LOCAL_INDEX`): the CLI indexes and
  searches a local SQLite file. No server, no Postgres, no network unless an
  external embedding provider is configured.
- **Client/server**: `semidx serve` runs the HTTP API backed by
  PostgreSQL/pgvector. The CLI (`login`, `search`, `sgrep`, `repo add`) and the
  MCP server are thin clients that talk to it over HTTP with a bearer token.

Both paths share the same indexing pipeline, chunker, extractor, embedding chain
and search service — only the persistence layer differs (see the store split
below).

## Tenant, workspace and project model

PostgreSQL is organized as `tenant -> workspace -> project -> indexed content`.
Projects,
tokens, jobs, conversations, credentials and the dependency catalog carry the
tenant boundary. API tokens resolve a default tenant and may select another
tenant with `X-Semidx-Tenant` only after membership verification; the selector
is never authority by itself. `X-Semidx-Workspace` selects a workspace already
belonging to that tenant; otherwise the compatibility `default` workspace is
used. Background workers carry the job tenant context before loading a project,
so asynchronous indexing cannot cross tenant boundaries while resolving
credentials or writing chunks.

The default tenant and workspace keep existing self-hosted installations compatible. The
same REST contract is used by the CLI, MCP and admin UI, while SQLite remains a
single-tenant standalone backend. This is the SaaS isolation seam without
forcing local users to run a service.

## Dependency catalog and cross-project relationships

Indexing records two complementary layers:

```
manifest declarations  ──▶ normalized project_dependencies rows
source imports          ──▶ file_dependencies graph
                              │
                              ├─ same package in another project
                              └─ Graph-RAG file-to-file expansion
```

`internal/depcatalog` parses declarations from `go.mod`, `package.json`,
`pom.xml`, Gradle scripts, `Package.swift`, `Package.resolved`, `Podfile` and
`Podfile.lock`. Constraints and exact lockfile versions are kept separately;
parsing does not claim that a build tool resolved a version. The optional
`DependencyStore` is implemented by PostgreSQL and SQLite, and the CLI/API
expose both the catalog and projects that share a normalized dependency. Real
Maven/Gradle/Swift/CocoaPods resolution belongs behind this same contract, so
`semidx deps resolve` runs native tools explicitly in local mode because they
may access the network or mutate a workspace. Managed worker/customer-agent
execution is available through the SaaS API.

Runtime communication is a separate graph rather than an inferred extension
of source imports:

```
 source imports      ──▶ file_dependencies       (static, source-derived)
 manifest packages  ──▶ project_dependencies    (declared/resolved catalog)
 telemetry/agent     ──▶ runtime_edges           (observed, aggregated)
```

`runtime_edges` can point to another project in the current tenant/workspace or
to an external service. The API accepts normalized counters instead of raw
traces, which keeps the first increment provider-neutral and safe to submit
from a customer agent. The portfolio endpoint exposes observed communication
without claiming it proves a source-level import.

Project privacy is persisted as `cloud`, `hybrid`, or `edge`. The indexer uses
the policy to force local providers for `edge` projects and retains the
sensitive-file guard for every mode. The same contract is available through
REST, SDK, CLI and the admin UI.

`tenant_quotas` is the first SaaS operations seam. It stores plan-independent
limits for projects and observed runtime edges; zero means unlimited. Checks
live at project creation and runtime ingestion, so billing and entitlements can
be added later without coupling them to the indexing or graph stores.

## Component map

```
 cmd/semidx ............ cobra CLI: login, index, search, sgrep, repo, skills,
                         models, drop, serve, mcp; global --local / --keyword.
 internal/config ....... resolves SEMIDX_* env (+ .env) into a Config.
 internal/clientconfig . ~/.config/semidx/config.yaml: server URL, token.
 internal/embed ........ Embedder interface + ChainEmbedder (fallback chain);
                         Ollama and OpenAI-compatible provider clients.
 internal/privacy ...... classifies files as sensitive (never sent to cloud).
 internal/extract ...... PDF/DOCX/XLSX/HTML/text -> plain text (pure Go).
 internal/chunker ...... file eligibility + splitting into line-ranged chunks.
 internal/indexing ..... walks a project, chunks, embeds, stores (with retry).
 internal/search ....... one search flow (embed query -> vector search ->
                         keyword fallback), shared by CLI, MCP and admin UI;
                         cross-project RRF fusion.
 internal/depcatalog ... normalized manifest adapters for Go, npm, Maven,
                         Gradle, Swift Package Manager and CocoaPods.
 internal/store ........ Store / IndexStore interfaces; PgStore (pgvector).
 internal/localstore ... SQLiteStore: standalone IndexStore, no CGO.
 internal/gitsync ...... clone/pull a git repo into the server's data dir.
 internal/server ....... HTTP API: auth, tenants, projects, jobs, files,
                         search and dependency comparison.
 internal/jwtauth ...... mint/verify HS256 control tokens (revocable via jti).
 internal/passwd ....... argon2id password hashing for web-UI users.
 internal/webadmin ..... server-rendered /admin UI (sessions + CSRF).
 internal/mcpserver .... thin MCP server; every tool is an HTTP client call.
 internal/skills ....... embedded agent skills, written by `skills install`.
 pkg/client ............ public Go SDK for the HTTP API (its own DTOs),
                         including tenant and multi-project search selection.
```

## The embedding chain and privacy routing

Embedding providers sit behind a single `Embedder` interface. `ChainEmbedder`
composes them in preference order and tries each until one succeeds:

1. A custom provider from `EMBED_PROVIDER` (`openai` or `ollama`), if set — this
   is prepended ahead of the defaults.
2. Cloud providers whose keys are present: Gemini, Groq, OpenRouter,
   Ollama Cloud (all via the OpenAI-compatible client).
3. **Local Ollama, always last** as the offline fallback.

Two switches restrict the chain to *local* providers only:

- **Privacy mode** — `EMBED_PRIVACY=true` or `--privacy`, applied to the whole
  run.
- **Per-file force-local** — carried on the `context` for a single sensitive
  file, so one file can be routed local without changing the run's mode.

The indexer routes each file:

```
 file ──▶ privacy.IsSensitive(path)?
            │
      no ───┼─── yes
            │        │
            ▼        ▼
   embed via     local provider available for the model?
   the chain          │
   (cloud ok)   yes ──┼── no
                      │      │
                      ▼      ▼
              embed locally  store text-only (embedding = NULL):
                             still keyword-searchable, never sent to a cloud
```

Sensitive = `.env`/`.pem`/`.key`/`.conf`/`.config` extensions, or any path
segment containing `env`, `secret`, `key`, `password`, `credential`, `token`,
`auth`, `config`, `db`, `database`, `private`, `pem`, `jwks`, `cert`, `ssl`.
Text-only chunks are stored with a `NULL` embedding, so they are excluded from
vector similarity but still returned by the keyword (ILIKE/LIKE) path.

**Keyword-only mode** (`SEMIDX_EMBED_MODE=none` / `--keyword`) skips the chain
entirely: every chunk is stored text-only and search runs purely on keyword
matching. `Fallback` is not set in this mode — it is the chosen strategy, not a
degradation.

## The store split: `IndexStore` vs `Store`

`internal/store` defines two interfaces so the standalone backend does not have
to implement server-only concerns:

- **`IndexStore`** — the persistence subset needed for indexing and search:
  project/file/chunk lifecycle, `SearchSimilar`, `SearchSimilarKeywords`,
  `EnsureChunksTable`, `DropAll`. Implemented by **both** `PgStore` and
  `SQLiteStore`.
- **`Store`** — embeds `IndexStore` and adds the server-only surface: API
  tokens, JWT tokens, users, web sessions and durable jobs. Implemented **only**
  by `PgStore`.

The CLI's index/search path depends on `IndexStore`, so the same code runs
against Postgres or SQLite. The server depends on `Store`.

### PostgreSQL / pgvector (server, and `semidx index`)

- **Dynamic per-dimension tables.** Chunk vectors live in `chunks_<dims>` tables
  (e.g. `chunks_1024`, `chunks_3072`), created on demand by `EnsureChunksTable`.
  A model's dimension picks the table, so models of different widths coexist
  without schema churn. The dimension is validated (1..16000) before it is
  formatted into the identifier.
- **HNSW cosine index, with a halfvec fallback.** pgvector's HNSW over the
  `vector` type maxes out at 2000 dimensions. At or below that, the index uses
  `vector_cosine_ops`; above it (e.g. 3072-dim models) the index and the query
  both use the `halfvec` cast (`halfvec_cosine_ops`) so the ANN index is still
  used.
- **Keyword fallback** is `ILIKE` over chunk content, scored a constant `0.5`.
- The static `projects` and `files` tables plus tokens/users/sessions/jobs are
  created by embedded goose migrations applied on connect.

### SQLite (standalone)

- One `chunks` table holds vectors of any dimension (SQLite is dynamically
  typed); embeddings are stored as little-endian `float32` BLOBs.
- Similarity search is a **brute-force cosine scan in Go** over the project's
  chunks — O(n) per query, which is fine at laptop / single-repo scale. For a
  large corpus, use the server (pgvector HNSW) instead.
- Pure Go (`modernc.org/sqlite`), so no CGO and a fully static binary.

## Document extraction

`internal/extract` turns documents into text before chunking, dispatching on the
file extension: `.txt`/`.md`/`.markdown` pass through (rejecting non-UTF-8
bytes), `.html`/`.htm` are flattened, `.pdf` yields its text layer, `.docx`
reads `word/document.xml`, `.xlsx` renders each sheet as tab-separated rows. All
decoders are pure Go. Encrypted/password-protected Office and PDF files are
detected (OLE2 magic / PDF encryption errors) and skipped, not treated as
failures. A panic in any third-party decoder is recovered into an error so one
bad document can never crash the indexer.

The incremental check hashes the *raw* bytes, so an unchanged document is
skipped before paying the extraction cost.

## Authentication

Three credential types, all resolved server-side:

- **Opaque API keys** — random `semidx_<hex>` tokens. Only the SHA-256 hash is
  stored; the plaintext is shown once. Sent as `Authorization: Bearer <token>`.
  Carry a scope set (`read`, `write`, or `admin`).
- **JWT control tokens** — HS256 tokens signed with `SEMIDX_JWT_SECRET`, minted
  from the web UI. Each carries a unique `jti` that is recorded server-side, so a
  token is revocable even if it never expires. Verification checks signature,
  algorithm and expiry, then looks up the `jti` for revocation. Disabled when no
  secret is set.
- **Web sessions** — for the `/admin` UI: a cookie-backed, server-side session
  (only the token hash is stored). Passwords are hashed with **argon2id**. Every
  mutating request carries a CSRF token bound to the session, and logins are
  rate-limited.

On an API request the bearer is resolved to a scope set; a route requiring
`write` accepts a token with `write` or `admin`. The `authed` middleware maps
missing/invalid tokens to 401, insufficient scope to 403.

### Outbound git credentials (secretbox)

The credentials above are all **inbound** (clients proving themselves to
semidx) and therefore stored as one-way hashes. Server-side git-sync needs the
opposite: credentials semidx presents **to a remote git host**, which must be
recoverable in cleartext. Those live in a separate vault
(`internal/secretbox` + `store.GitCredentialStore`): AES-256-GCM under a key
derived (HKDF-SHA256, versioned for rotation) from the operator's
`SEMIDX_SECRET_KEY` master key. Each stored credential is scoped to a
**project** or a **host** (HTTPS token/password or SSH private key + optional
pinned `known_hosts`).

**Current sync path** still uses only the global `SEMIDX_GIT_TOKEN` /
`SEMIDX_GIT_USER` env (see self-hosting). Project → host → env resolution in the
job runner is a follow-up on top of this vault. SSH transport needs
`openssh-client` in the image (the Dockerfile installs it alongside `git`);
when an SSH credential is used, the key is materialised as an ephemeral `0600`
file only while the git command runs. With `SEMIDX_SECRET_KEY` unset the vault
is disabled.

## Durable indexing jobs

Git and re-index work runs as durable jobs, not in the request:

```
 POST …/index-jobs ──▶ INSERT index_jobs (status=queued)
                                   │
        server workers (StartWorkers) poll every 2s
                                   │
        ClaimJob: UPDATE … WHERE id = (SELECT … FOR UPDATE SKIP LOCKED LIMIT 1)
                                   │
        git project? gitsync.Sync (shallow clone / ff-only pull) into DATA_DIR
                                   │
        index the checkout ──▶ CompleteJob (counts) | FailJob (error)
```

`FOR UPDATE SKIP LOCKED` means two workers never grab the same job and a queued
job survives a restart. `GET /api/v1/jobs/{id}` returns its status and result
counts. Job types are `full` and `git_history`.

## Search flow (shared)

`internal/search.Service` is the single implementation used by the CLI (embedded
mode), the MCP server and the admin UI:

1. Resolve the project and its model (an explicit `--model` overrides it).
2. Resolve the dimension: a provider that knows the model wins; otherwise it is
   inferred from the model name.
3. Embed the query and run vector search (`SearchSimilar`).
4. If embedding fails, fall back to keyword search and set `Fallback = true` so
   the caller can warn that results are literal, not semantic.

In remote mode the CLI skips this and calls the server's search endpoint, then
renders the response through the *same* formatters, so `file:line:content`
output is identical whether the index is local or on a server.
