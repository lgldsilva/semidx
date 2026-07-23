# HTTP API (v1)

The server (`semidx serve`) exposes a small REST API under `/api/v1`, plus
unauthenticated health and metrics endpoints. All request and response bodies
are JSON. A Go SDK for this surface lives in [`pkg/client`](../pkg/client).

## Versioning

The API is versioned via the URL path (`/api/v1/`). Within the same major version
semidx guarantees **no breaking changes** (no removed or renamed fields, no
changed semantics, no new required request fields). The following changes are
**not** considered breaking and may appear in any minor/patch release:

- Adding new optional request fields (ignored by older clients).
- Adding new response fields (older clients safely ignore unknown JSON keys).
- Adding new endpoints.
- Changing the order of enum values in responses.

The version number does **not** track the semidx binary version — an `x.y.z`
binary release may carry the same `/api/v1` surface. A theoretical `/api/v2`
would only appear when breaking changes are unavoidable, and the old `/api/v1`
would be deprecated but kept for at least one full release cycle.

## Authentication

Authenticated endpoints require a bearer token:

```
Authorization: Bearer <token>
```

Two token types are accepted, both resolving to a set of scopes:

- **Opaque API keys** — random `semidx_<hex>` strings. Only their SHA-256 hash
  is stored server-side; the plaintext is shown once when created (via the
  `/admin` UI or the bootstrap flow).
- **JWT control tokens** — HS256 tokens minted from the `/admin` UI when
  `SEMIDX_JWT_SECRET` is set. Each carries a unique `jti` recorded server-side,
  so it is revocable even when non-expiring.

### Scopes

Every authenticated route requires one scope. A token with `admin` satisfies any
requirement.

| Scope | Grants |
|---|---|
| `read` | search, list/get projects, get job status |
| `write` | create/delete projects, enqueue jobs, push files |
| `admin` | everything (also used by the bootstrap token) |

### Error shape

Non-2xx responses carry a JSON error object:

```json
{ "error": "human-readable message" }
```

Common statuses: `400` invalid body / missing field, `401` missing or invalid
token, `403` token missing the required scope, `404` unknown project/job, `409`
project already exists, `500`/`502` server or upstream (embedding) failure.

## Endpoints

| Method | Path | Scope | Purpose |
|---|---|---|---|
| GET | `/healthz` | none | Liveness. |
| GET | `/readyz` | none | Readiness (pings the database). |
| GET | `/metrics` | none | Prometheus metrics. |
| GET | `/api/v1/tenants` | `read` | List organizations visible to the principal. |
| POST | `/api/v1/tenants` | `admin` | Create an organization and its default workspace. |
| GET | `/api/v1/workspaces` | `read` | List workspaces in the active tenant. |
| POST | `/api/v1/workspaces` | `admin` | Create a workspace in the active tenant. |
| POST | `/api/v1/projects` | `write` | Create a project. |
| GET | `/api/v1/projects` | `read` | List projects. |
| GET | `/api/v1/projects/{project}` | `read` | Get one project. |
| DELETE | `/api/v1/projects/{project}` | `write` | Delete a project. |
| POST | `/api/v1/projects/{project}/search` | `read` | Semantic search. |
| POST | `/api/v1/search` | `read` | Search selected projects or the whole active workspace with RRF fusion. |
| GET | `/api/v1/projects/{project}/dependencies` | `read` | List normalized manifest dependencies. |
| GET | `/api/v1/projects/{project}/dependencies/shared` | `read` | Find matching dependencies in other workspace projects. |
| POST | `/api/v1/projects/{project}/dependencies/resolve` | `write` | Queue managed resolution or return the customer-agent contract. |
| POST | `/api/v1/projects/{project}/dependencies/submit` | `write` | Atomically submit a customer-agent resolution result. |
| POST | `/api/v1/projects/{project}/index-jobs` | `write` | Enqueue an index job. |
| POST | `/api/v1/projects/{project}/files/diff` | `write` | Diff client files vs. the index. |
| POST | `/api/v1/projects/{project}/files/batch` | `write` | Upload files to index. |
| GET | `/api/v1/jobs/{id}` | `read` | Get an index job's status. |

`{project}` is the project name (URL-escaped); `{id}` is a numeric job id.
`X-Semidx-Tenant` and `X-Semidx-Workspace` are selectors, not authorities; the
bearer principal must be allowed to use both scopes.

### Multi-project search

`POST /api/v1/search` searches `projects` by display name, or all projects when
`all` is true:

```json
{
  "query": "where is authentication configured?",
  "projects": ["api", "worker"],
  "top_k": 10,
  "graph": true,
  "max_per_file": 2
}
```

Results include `project` provenance, the original similarity `score`, and a
`fusion_score`. The latter is the reciprocal-rank-fusion score used to compare
projects with different embedding models or score distributions. `keyword`,
`fallback`, `degraded`, `project_count` and `skipped_count` make degraded or
partial searches explicit.

### Dependency comparison

`GET /api/v1/projects/{project}/dependencies` returns normalized declarations
with `ecosystem`, `name`, `constraint`, optional `resolved_version`, `scope` and
`manifest`. `GET .../dependencies/shared` returns the same package identity in
other projects of the active workspace. Matching uses `(ecosystem,
normalized_name)`; versions and scopes remain visible so the UI can distinguish
the same library used at different constraints.

### Dependency resolution

`POST /api/v1/projects/{project}/dependencies/resolve` accepts `{ "mode":
"managed" }` (the default) and queues a `resolve_dependencies` worker job for a
server-accessible checkout. Use `{ "mode": "agent" }` when source and package
credentials must stay on the customer machine; the response is an
`awaiting_agent` contract and does not execute tools on the server.

The customer agent submits only normalized metadata, never source files:

```json
{
  "source": "customer-agent",
  "dependencies": [
    { "ecosystem": "maven", "name": "org.slf4j:slf4j-api", "resolved_version": "2.0.13", "scope": "compile", "manifest": "pom.xml", "direct": true }
  ]
}
```

`POST /api/v1/projects/{project}/dependencies/submit` replaces the catalog in a
single transaction. Supported native ecosystems are Go, npm, Maven, Gradle,
Swift and CocoaPods. The CLI maps these contracts to `semidx deps resolve
PROJECT --mode managed|agent`.

### Health and metrics

- `GET /healthz` → `200` with body `ok`.
- `GET /readyz` → `200` body `ready`, or `503` `{"error":"database not ready"}`.
- `GET /metrics` → Prometheus exposition format, including
  `semidx_http_requests_total{method,status}`.

### Create a project

`POST /api/v1/projects` (scope `write`)

```json
{
  "name": "myapp",
  "model": "bge-m3",
  "source": { "type": "git", "url": "https://github.com/acme/myapp.git", "branch": "main" }
}
```

- `model` defaults to `bge-m3` if omitted.
- `source.type` is `push` (clients upload files) or `git` (server clones
  `source.url`); it defaults to `push`. `source.url` is required for `git`.

`201 Created` returns the project:

```json
{
  "name": "myapp",
  "model": "bge-m3",
  "status": "registered",
  "source_type": "git",
  "git_url": "https://github.com/acme/myapp.git",
  "branch": "main"
}
```

`409 Conflict` if the name is already taken. (`git_url` and `branch` are omitted
when empty.)

### List / get projects

`GET /api/v1/projects` (scope `read`):

```json
{ "projects": [ { "name": "myapp", "model": "bge-m3", "status": "ready", "source_type": "git", "git_url": "https://github.com/acme/myapp.git", "branch": "main" } ] }
```

`GET /api/v1/projects/{project}` (scope `read`) returns a single project object
of the same shape, or `404`.

### Delete a project

`DELETE /api/v1/projects/{project}` (scope `write`) → `204 No Content` (files and
chunks cascade). `404` if unknown.

### Search

`POST /api/v1/projects/{project}/search` (scope `read`)

```json
{ "query": "verify auth token", "top_k": 5, "model": "", "graph": false, "graph_depth": 2 }
```

- `query` is required.
- `top_k` defaults to 5 when `<= 0`.
- `model` optionally overrides the project's stored model.
- `graph` (default `false`) enables Graph-RAG expansion along the
  import/dependency graph.
- `graph_depth` bounds the expansion BFS depth when `graph` is true; it is
  clamped to the server maximum (5) and `<= 0` uses the default.

`200 OK`:

```json
{
  "project": "myapp",
  "model": "bge-m3",
  "fallback": false,
  "took_ms": 42,
  "results": [
    {
      "path": "internal/auth/verify.go",
      "start_line": 12,
      "end_line": 34,
      "score": 0.87,
      "content": "func Verify(...) {",
      "stale": false,
      "indexed_at": "2026-07-22T12:00:00Z"
    }
  ]
}
```

- `fallback` is `true` when embedding was unavailable and the server used keyword
  search (results are literal, not semantically ranked).
- `took_ms` is the server-side wall-clock time for the search in milliseconds,
  including both the embedding call (when applicable) and the database query.
  Use it for latency monitoring but do not rely on its precision for
  benchmarking — it is measured with `time.Since` and reflects coarse
  application-level timing.
- `stale` (boolean, additive, best-effort) is `true` when the hit's on-disk
  content hash no longer matches the indexed `files.hash`. When the check cannot
  run (missing path, list error, remote without a readable tree), the field is
  `false` — search never fails because of staleness. Text/CLI output may also
  mark the hit `[stale]` with a "re-read before editing" note.
- `indexed_at` (RFC3339 string when known, omitempty/null when unknown) is when
  that file version was last indexed.
- `404` if the project does not exist.

### Enqueue an index job

`POST /api/v1/projects/{project}/index-jobs` (scope `write`)

```json
{ "type": "full" }
```

- `type` is `full` (default) or `git_history`. An empty body defaults to `full`.

`202 Accepted`:

```json
{ "job_id": 7, "status": "queued" }
```

`400` for an invalid type, `404` for an unknown project.

### Get a job

`GET /api/v1/jobs/{id}` (scope `read`)

```json
{
  "id": 7,
  "type": "full",
  "status": "succeeded",
  "files_indexed": 128,
  "chunks_created": 512,
  "deleted_files": 1,
  "error_count": 0
}
```

`status` is `queued`, `running`, `succeeded` or `failed`; `error` is present
(and non-empty) only for a failed job. `deleted_files` and `error_count` are
non-zero only for batch (push) jobs. `404` if unknown.

### Push files (diff + batch)

These power a client that uploads file contents for the server to chunk and
embed (so provider credentials stay on the server). Used by the SDK; the CLI
does not yet expose a push subcommand.

`POST /api/v1/projects/{project}/files/diff` (scope `write`) — report which of
the client's files are stale (new/changed) or deleted:

```json
{ "files": { "main.go": "<sha256>", "README.md": "<sha256>" } }
```

```json
{ "stale": ["main.go"], "deleted": ["old.go"] }
```

`POST /api/v1/projects/{project}/files/batch` (scope `write`) — upload contents
to index and remove deleted paths.

By default the endpoint returns `202 Accepted` with a `job_id` and the batch
is processed asynchronously by a background worker. The old synchronous
behaviour (returning `200 OK` with inline counts) is available by adding
`?sync=true` to the URL.

**Async (default):**

```json
{
  "files": [ { "path": "main.go", "content": "package main\n..." } ],
  "delete": [ "old.go" ]
}
```

`202 Accepted`:

```json
{ "job_id": 7, "status": "queued" }
```

Use `GET /api/v1/jobs/{id}` to poll for completion. The job's `status` will be
`queued` → `running` → `succeeded` (or `failed`). On success, `files_indexed`,
`chunks_created`, `deleted_files` and `error_count` are populated.

Async is only supported for push projects (`source_type: "push"`); other project
types must use `?sync=true`.

**Sync (`?sync=true`):**

```json
{
  "files": [ { "path": "main.go", "content": "package main\n..." } ],
  "delete": [ "old.go" ]
}
```

`200 OK`:

```json
{ "indexed": 1, "chunks": 6, "deleted": 1, "errors": 0 }
```

### Project privacy policy

Projects persist a `privacy_mode`: `hybrid` (default), `cloud`, or `edge`.
Hybrid keeps the existing sensitive-file routing; cloud is cloud-first for
ordinary files; edge forces local providers and falls back to text-only
keyword-searchable chunks when no local model is available.

Use `PUT /api/v1/projects/{project}/privacy` (scope `write`) with
`{ "mode": "edge" }`. Managed jobs and push ingestion apply the policy. The
CLI equivalent is `semidx privacy PROJECT --mode edge`.

### Observed runtime graph

Static imports answer what source declares; runtime edges answer what a
deployment actually called. A customer agent or telemetry adapter can submit
normalized observations without uploading source files:

`POST /api/v1/projects/{project}/runtime-edges` (scope `write`)

```json
{
  "edges": [
    {
      "target_project": "payments",
      "source_component": "checkout",
      "target_component": "charge",
      "protocol": "grpc",
      "environment": "prod",
      "request_count": 120,
      "error_count": 2,
      "p95_latency_ms": 84.5
    }
  ]
}
```

`target_project` may be another indexed project or an external service.
Repeated observations aggregate by source/target/component/protocol/
environment. Read one project with `GET /api/v1/projects/{project}/runtime-edges`
or the active portfolio with `GET /api/v1/runtime-graph?limit=500` (scope
`read`). CLI equivalents: `semidx graph runtime PROJECT --input telemetry.json`
and `semidx graph portfolio`.

### Tenant quotas and usage

The first SaaS operations seam is persisted per tenant. `tenant_quotas` supports
`plan`, `max_projects`, and `max_runtime_edges`; zero means unlimited. Project
creation and runtime-edge submission enforce these limits when the store
supports the quota contract. Billing can map plans to the same limits later
without changing indexing or graph APIs.

## MCP tools

`semidx mcp` runs a thin MCP server over stdio. In **remote** mode it proxies the
HTTP API above; in **standalone/local** mode it talks to the local index
directly. Tools always register; behavior that needs a local tree returns an
in-band message instead of failing the whole server.

### Search & project tools (standalone and remote)

| Tool | Args | Behavior |
|---|---|---|
| `semantic_search` | `project?`, `query`, `top_k?`, `model?`, `graph?`, `graph_depth?`, `format?` | Semantic search; each hit may include best-effort `stale` + `indexed_at` (see Search). Text/`format=text` output prefixes stale hits with `[stale]`. |
| `semantic_status` | `project?` | Indexing status for a registered project (file count, status, model). |
| `semantic_projects` | _(none)_ | List registered projects and their indexing status. |
| `semantic_reindex` | `project?`, `type?` (`full` \| `git_history`, default `full`) | **Server-only:** enqueue a re-index job for a registered project. In standalone mode returns an in-band tip to run `semidx index`. |
| `semantic_ask` | `project?`, `question`, `top_k?` | RAG-augmented Q&A over indexed chunks with citations. |

### Code-intelligence tools (standalone/local only)

These read the local dependency graph and tree-sitter symbols. Against a remote
server they return an in-band **"standalone/local mode only"** message.

| Tool | Args | Behavior |
|---|---|---|
| `semantic_callers` | `project?`, `file`, `line` | Files that import/depend on the package containing the symbol at `file:line` ("who calls this?"). CLI: `semidx callers <file:line>`. |
| `semantic_explain` | `project?`, `file`, `line` | Symbol kind/location, dependencies, importers, related tests. CLI: `semidx explain <file:line>`. |
| `semantic_impact` | `project?`, `file`, `line`, `depth?` (default 5, max 10) | Blast radius: transitive reverse deps of the symbol, tagged by depth. **MCP-only** (no CLI command today). |
| `semantic_deadcode` | `project?` | Unused symbols, classified `confirmed` (safe-to-delete unexported) / `public-api` (exported — review). CLI: `semidx dead-code`. |
| `semantic_diff` | `ref_range` (`ref1..ref2` or `ref1...ref2`) | New / removed / changed-signature symbols between two git refs. CLI: `semidx diff <ref1>..<ref2>`. |

Bundled agent skills (`semidx skills install`) include `code-intel` (when to use
the structural tools vs search/grep) and `impact-before-refactor` (run
`semantic_impact` before editing an unfamiliar symbol), alongside
`semantic-search`, `auto-index`, and `workspace-agent`.

## Notes

- All handlers accept and return `application/json`.
- The server has a `ReadHeaderTimeout` and shuts down gracefully on
  SIGINT/SIGTERM.
- The MCP server (`semidx mcp`) is a thin client over these same endpoints for
  search/list/reindex; the code-intelligence tools above are local-index only
  and do not map to HTTP routes yet.
