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
| POST | `/api/v1/projects` | `write` | Create a project. |
| GET | `/api/v1/projects` | `read` | List projects. |
| GET | `/api/v1/projects/{project}` | `read` | Get one project. |
| DELETE | `/api/v1/projects/{project}` | `write` | Delete a project. |
| POST | `/api/v1/projects/{project}/search` | `read` | Semantic search. |
| POST | `/api/v1/projects/{project}/index-jobs` | `write` | Enqueue an index job. |
| POST | `/api/v1/projects/{project}/files/diff` | `write` | Diff client files vs. the index. |
| POST | `/api/v1/projects/{project}/files/batch` | `write` | Upload files to index. |
| GET | `/api/v1/jobs/{id}` | `read` | Get an index job's status. |

`{project}` is the project name (URL-escaped); `{id}` is a numeric job id.

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
{ "query": "verify auth token", "top_k": 5, "model": "" }
```

- `query` is required.
- `top_k` defaults to 5 when `<= 0`.
- `model` optionally overrides the project's stored model.

`200 OK`:

```json
{
  "project": "myapp",
  "model": "bge-m3",
  "fallback": false,
  "took_ms": 42,
  "results": [
    { "path": "internal/auth/verify.go", "start_line": 12, "end_line": 34, "score": 0.87, "content": "func Verify(...) {" }
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
  "chunks_created": 512
}
```

`status` is `queued`, `running`, `succeeded` or `failed`; `error` is present
(and non-empty) only for a failed job. `404` if unknown.

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
to index and remove deleted paths:

```json
{
  "files": [ { "path": "main.go", "content": "package main\n..." } ],
  "delete": [ "old.go" ]
}
```

```json
{ "indexed": 1, "chunks": 6, "deleted": 1, "errors": 0 }
```

## Notes

- All handlers accept and return `application/json`.
- The server has a `ReadHeaderTimeout` and shuts down gracefully on
  SIGINT/SIGTERM.
- The MCP server (`semidx mcp`) is a thin client over these same endpoints: its
  `semantic_search`, `semantic_projects` and `semantic_reindex` tools map to
  search, list-projects and enqueue-job respectively.
