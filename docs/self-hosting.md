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

The image (`Dockerfile`) builds a static binary and runs it on Alpine with
`git` and `openssh-client` (for server-side git-sync over HTTPS and SSH) and
CA certificates (for cloud embedders). The container entrypoint is
`semidx serve`.

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
| `SEMIDX_DB_PASSWORD` | `changeme` | Database password used by the compose file to build the DSN (`postgres://semidx:${SEMIDX_DB_PASSWORD}@…`). Not read directly by `semidx serve` — set it in the environment before `docker compose up`. |

### Server

| Variable | Default | Purpose |
|---|---|---|
| `SEMIDX_LISTEN_ADDR` | `:8080` | Bind address for the HTTP API. |
| `SEMIDX_DATA_DIR` | `/var/lib/semidx` | Where the server clones git projects (under `<dir>/repos/<name>`). |
| `SEMIDX_BOOTSTRAP_TOKEN` | *(unset)* | Fixes the first admin API token instead of generating one. Only used on an empty database. |
| `SEMIDX_BOOTSTRAP_ADMIN_USER` | `admin` | Username of the bootstrap web-admin user. |
| `SEMIDX_BOOTSTRAP_ADMIN_PASSWORD` | *(unset)* | Password for the bootstrap web-admin user. No user is created if empty. |
| `SEMIDX_COOKIE_SECURE` | `true` | Sets the `Secure` flag on admin cookies. Set to `false` **only** for plain-HTTP local testing. |
| `SEMIDX_JWT_SECRET` | *(unset)* | HS256 signing key for JWT control tokens. When empty, control tokens are disabled and the UI hides the feature. |
| `SEMIDX_CSRF_KEY` | *(auto)* | HMAC key for web-admin CSRF tokens. When empty, a random key is generated on each restart. Set it to a persistent value so tokens survive server restarts. |
| `SEMIDX_SECRET_KEY` | *(unset)* | Master key for the AES-256-GCM vault that encrypts stored git credentials (see below). Exactly 32 bytes, hex or base64. When empty, the credential vault is disabled. Generate with `openssl rand -hex 32`. |

### Server-side git sync (`repo add` / reindex)

| Variable | Default | Purpose |
|---|---|---|
| `SEMIDX_GIT_SSL_NO_VERIFY` | `false` | When `true`, disables TLS verification for server-side `git clone`/`pull` (self-signed homelab Gitea). Prefer installing a real CA when possible. |
| `SEMIDX_GIT_TOKEN` | *(unset)* | PAT for private HTTPS clones; injected as an Authorization header (not stored in the repo URL). Falls back to `SEMIDX_GITHUB_TOKEN`. |
| `SEMIDX_GIT_USER` | `x-access-token` | Basic-auth username paired with `SEMIDX_GIT_TOKEN` (Gitea/GitHub default). |
| `SEMIDX_GIT_ALLOW_FILE` | `false` | When `true`, permits `file://` git URLs for server-side sync. |

#### Stored git credentials (per project / per host)

Beyond the single global `SEMIDX_GIT_TOKEN`, the server can store **individual
git credentials** — an HTTPS token/password or an SSH private key — scoped
either to one project or to a whole host (e.g. everything on
`gitea.example.com`). Resolution order when a repo needs to be cloned/pulled:
the project's own credential, then the credential registered for its host,
then the global `SEMIDX_GIT_TOKEN` env fallback.

Stored credentials must be recoverable (the server has to hand them to `git`),
so they are encrypted at rest with **AES-256-GCM** in a vault keyed by
`SEMIDX_SECRET_KEY` (the master key never encrypts directly — the data key is
derived via HKDF-SHA256, versioned for future rotation). Requirements:

- Set `SEMIDX_SECRET_KEY` to a 32-byte key (`openssl rand -hex 32`) and keep
  it stable — losing it makes every stored credential unreadable. Without it,
  the vault (and credential registration) is disabled; the global env token
  still works.
- SSH credentials additionally need the `ssh` binary in the server image — the
  official image ships `openssh-client` (and `git`) for exactly this. SSH keys
  are written to an ephemeral `0600` file only for the duration of the git
  command; an optional `known_hosts` entry per credential pins the host key
  (otherwise it is recorded on first contact under
  `SEMIDX_DATA_DIR/ssh/known_hosts.d/<host>`).

### Indexing & extraction

| Variable | Default | Purpose |
|---|---|---|
| `SEMIDX_INDEX_WORKERS` | `4` | Concurrent index workers / file concurrency. |
| `SEMIDX_EMBED_BATCH_SIZE` | `8` | Texts per embedding API call (positive int). |
| `SEMIDX_MAX_FILE_SIZE` | `1048576` (1 MB) | Largest file (bytes) the indexer processes; larger files are silently skipped. |
| `SEMIDX_MAX_CHUNKS_PER_FILE` | `32` | Maximum chunks a single file can produce. |
| `SEMIDX_JAVA_DECOMPILER` | *(unset)* | External Java decompiler command for `.class` files extracted from JAR archives. |

### Self-update (`semidx upgrade`)

Override when using a private release host (the defaults point at the homelab Gitea).

| Variable | Default | Purpose |
|---|---|---|
| `SEMIDX_UPDATE_API` | *(homelab Gitea)* | Releases API base URL for `semidx upgrade`. |
| `SEMIDX_UPDATE_URL` | *(homelab Gitea)* | Release download base URL for `semidx upgrade`. |
| `SEMIDX_UPDATE_TOKEN` | *(unset)* | Token for `semidx upgrade` against a private release host. |
| `SEMIDX_INSECURE` | *(unset)* | Skip TLS verification for update downloads (`1` for self-signed CA). |

### Embedding providers

Set the provider(s) you use. If none is set, only local Ollama is available.

| Variable | Default | Purpose |
|---|---|---|
| `SEMIDX_OLLAMA_URL` (or legacy `OLLAMA_URL`) | `http://localhost:11434` | Local Ollama endpoint (the always-present fallback). |
| `SEMIDX_OLLAMA_URLS` | *(unset)* | Comma-separated Ollama URLs for parallel embedding. Overrides `SEMIDX_OLLAMA_URL` when set. |
| `GEMINI_API_KEY` | *(unset)* | Enables Gemini's OpenAI-compatible embedding endpoint. |
| `SEMIDX_GEMINI_BASE_URL` | *(Gemini default)* | Gemini API base URL override. |
| `GROQ_API_KEY` | *(unset)* | Enables Groq. |
| `SEMIDX_GROQ_BASE_URL` | *(Groq default)* | Groq API base URL override. |
| `OPENROUTER_API_KEY` | *(unset)* | Enables OpenRouter. |
| `SEMIDX_OPENROUTER_BASE_URL` | *(OpenRouter default)* | OpenRouter API base URL override. |
| `OLLAMA_CLOUD_API_KEY` | *(unset)* | Enables Ollama Cloud. |
| `SEMIDX_OLLAMA_CLOUD_BASE_URL` | *(Ollama Cloud default)* | Ollama Cloud API base URL override. |
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
