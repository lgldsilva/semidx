# Self-hosting the semidx server

The server is `semidx serve`: an HTTP API plus a `/admin` UI, backed by
PostgreSQL with the `pgvector` extension. This guide covers running it with
Docker Compose, every environment variable it reads, TLS, backups and upgrades.

## Run it with Docker Compose

The reference deployment in `deploy/docker-compose.yml` brings up the server and
a `pgvector/pgvector:pg16` database. The database has **no published ports** — it
is reachable only on the internal compose network, so the only way in is the
authenticated API.

```bash
docker compose -f deploy/docker-compose.yml up -d --build
```

Set a database password (it defaults to `changeme`) and, optionally, a fixed
bootstrap token before the first start:

```bash
export SEMIDX_DB_PASSWORD='a-strong-db-password'
export SEMIDX_BOOTSTRAP_TOKEN='semidx_your_choice'   # optional
docker compose -f deploy/docker-compose.yml up -d --build
```

The image (`Dockerfile`) builds a static binary and runs it on Alpine with `git`
(for server-side git-sync) and CA certificates (for cloud embedders). The
container entrypoint is `semidx serve`.

> Note: `deploy/docker-compose.yml` is the server deployment. The
> `docker-compose.yml` at the repo root is a **development-only** database (it
> publishes Postgres on `55432` for running the CLI and tests locally) — do not
> use it to expose a production server.

## First-start bootstrap

On a database with no tokens/users yet, `serve` bootstraps credentials and logs
them once to stderr:

- **Bootstrap admin API token.** If `SEMIDX_BOOTSTRAP_TOKEN` is set it becomes
  that token; otherwise a random `admin`-scoped token is generated and printed
  (`bootstrap admin token (shown once — save it): …`). Save it — it is not shown
  again.
- **Bootstrap web admin user.** Created only if `SEMIDX_BOOTSTRAP_ADMIN_PASSWORD`
  is set, with username `SEMIDX_BOOTSTRAP_ADMIN_USER` (default `admin`).

```bash
docker compose -f deploy/docker-compose.yml logs semidx | grep -E "bootstrap"
```

Once at least one token (or user) exists, bootstrap is skipped on subsequent
starts.

## Environment variables

All configuration is read from the environment (a `.env` file in the working
directory is also honored; real environment variables win over it).

### Database

| Variable | Default | Purpose |
|---|---|---|
| `SEMIDX_DB_DSN` | `postgres://semantic:semantic@localhost:55432/semantic_indexer` | PostgreSQL/pgvector connection string. **Required** for the server; the compose file sets it to the internal `db` service. |

### Server

| Variable | Default | Purpose |
|---|---|---|
| `SEMIDX_LISTEN_ADDR` | `:8080` | Bind address for the HTTP API. |
| `SEMIDX_DATA_DIR` | `/var/lib/semidx` | Where the server clones git projects (under `<dir>/repos/<name>`). |
| `SEMIDX_INDEX_WORKERS` | `4` | Concurrent index workers / file concurrency. |
| `SEMIDX_BOOTSTRAP_TOKEN` | *(unset)* | Fixes the first admin API token instead of generating one. Only used on an empty database. |
| `SEMIDX_BOOTSTRAP_ADMIN_USER` | `admin` | Username of the bootstrap web-admin user. |
| `SEMIDX_BOOTSTRAP_ADMIN_PASSWORD` | *(unset)* | Password for the bootstrap web-admin user. No user is created if empty. |
| `SEMIDX_COOKIE_SECURE` | `true` | Sets the `Secure` flag on admin cookies. Set to `false` **only** for plain-HTTP local testing. |
| `SEMIDX_JWT_SECRET` | *(unset)* | HS256 signing key for JWT control tokens. When empty, control tokens are disabled and the UI hides the feature. |

### Embedding providers

Set the provider(s) you use. If none is set, only local Ollama is available.

| Variable | Default | Purpose |
|---|---|---|
| `SEMIDX_OLLAMA_URL` (or legacy `OLLAMA_URL`) | `http://localhost:11434` | Local Ollama endpoint (the always-present fallback). |
| `GEMINI_API_KEY` | *(unset)* | Enables Gemini's OpenAI-compatible embedding endpoint. |
| `GROQ_API_KEY` | *(unset)* | Enables Groq. |
| `OPENROUTER_API_KEY` | *(unset)* | Enables OpenRouter. |
| `OLLAMA_CLOUD_API_KEY` | *(unset)* | Enables Ollama Cloud. |
| `EMBED_PROVIDER` | *(unset)* | Prepends a custom provider to the chain: `openai` or `ollama`. |
| `EMBED_ENDPOINT` | *(auto)* | Endpoint for the custom provider (defaults to the OpenAI or Ollama URL). |
| `EMBED_API_KEY` | *(unset)* | Bearer key for the custom `openai` provider. |
| `EMBED_PRIVACY` | `false` | `true` forces local-only providers for the whole process. |
| `SEMIDX_EMBED_MODE` | *(unset)* | `none` selects keyword-only indexing/search (no embeddings). |

### Standalone / CLI (not used by the server)

| Variable | Default | Purpose |
|---|---|---|
| `SEMIDX_LOCAL_INDEX` | *(unset)* | A path (or a truthy value for the default location) to index into local SQLite instead of Postgres. |
| `SEMIDX_SERVER_URL` | *(unset)* | Overrides the logged-in server URL for the CLI. |
| `SEMIDX_TOKEN` | *(unset)* | Overrides the CLI's bearer token. |
| `SEMIDX_DEFAULT_PROJECT` | *(unset)* | Default project for search/sgrep. |

### Runtime

| Variable | Default | Purpose |
|---|---|---|
| `GOMEMLIMIT` | *(unset)* | Go soft heap limit (e.g. `900MiB`) so the runtime GCs before hitting a container memory cap. The compose file defaults it to `900MiB`. |

## Reverse proxy and TLS

Run the server behind a TLS-terminating reverse proxy (nginx, Caddy, Traefik).
Because the admin UI sets its session cookie with the `Secure` flag by default
(`SEMIDX_COOKIE_SECURE=true`), the browser will only send that cookie over
HTTPS — so the admin login works only when the UI is reached over TLS. Keep
`SEMIDX_COOKIE_SECURE=true` in production; set it to `false` **only** for a
local plain-HTTP test.

Expose only the API port (`:8080` by default). Never publish the Postgres port
in production — the compose file intentionally does not.

## Backups

Back up PostgreSQL with a custom-format dump:

```bash
docker compose -f deploy/docker-compose.yml exec db \
  pg_dump -Fc -U semidx semidx > semidx-$(date +%F).dump
```

What actually matters is **the project list and configuration** (projects,
their git URLs/branches, tokens, users). The chunk embeddings are *derived* data
— they can be regenerated by re-indexing from source — so your recovery point
objective can be loose: losing recent embeddings just means re-running index
jobs, not losing irreplaceable data. Restore with `pg_restore` into a fresh
database, then let the server re-index git projects as needed.

The `SEMIDX_DATA_DIR` volume holds git checkouts, which are re-clonable from
their origins; it does not need a strict backup.

## Upgrades

1. Pull the new image / rebuild: `docker compose -f deploy/docker-compose.yml up -d --build`.
2. Schema migrations are embedded in the binary and applied automatically on
   connect (goose), so no manual migration step is needed.
3. Embedding data is forward-compatible: existing `chunks_<dims>` tables are
   reused; new models simply create new per-dimension tables.

During early `v0.x` development the API and internals may still change between
releases — pin to a tag and read release notes before upgrading.
