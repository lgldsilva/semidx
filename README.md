# semidx

Self-hosted semantic search for your code and documents.

[![CI](https://github.com/lgldsilva/semidx/actions/workflows/ci.yml/badge.svg)](https://github.com/lgldsilva/semidx/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/lgldsilva/semidx)](https://github.com/lgldsilva/semidx/releases/latest)
[![go](https://img.shields.io/badge/go-1.26.5%2B-blue.svg)](go.mod)
[![license](https://img.shields.io/badge/license-Apache--2.0-lightgrey.svg)](LICENSE)
[![install.sh](https://img.shields.io/badge/install.sh-live-brightgreen.svg)](install.sh)
[![install.ps1](https://img.shields.io/badge/install.ps1-live-brightgreen.svg)](install.ps1)
[![GHCR](https://img.shields.io/badge/GHCR-live-brightgreen.svg)](https://github.com/lgldsilva/semidx/pkgs/container/semidx)

`semidx` indexes a codebase (and documents like PDF, DOCX, XLSX, HTML) into a
vector store and searches it by *meaning*, not literal text. A query like
"where is the retry backoff implemented" finds the code even when it never
contains the words "retry" or "backoff".

It runs two ways from one binary:

- **Central server** — one shared index that a whole team (and their AI agents)
  query over an authenticated HTTP API. Embeddings, provider API keys and the
  database all live on the server; clients never touch them.
- **Standalone** — a single machine indexing into a local SQLite file, with no
  server and no database to run.

## The niche it fills

Most "semantic code search" tools are either a hosted SaaS you send your code
to, or a local-only helper with no sharing story. semidx is deliberately the
**self-hosted middle**:

- **Central and shared.** Index a repository once on the server; every developer
  and agent searches the same index. No per-machine re-indexing.
- **Credentials stay put.** Embedding provider keys live only on the server (or
  only on your laptop, in standalone mode). Clients send text to *your* server,
  not to a third party.
- **Privacy routing built in.** Files that look like secrets (`.env`, `.pem`,
  paths containing `secret`/`key`/`token`/`password`, …) are never sent to a
  cloud embedding provider — they are embedded locally, or stored as
  keyword-only text if no local model is available.
- **Git-sync.** Point the server at a git URL; it clones, indexes, and
  re-indexes on demand — clients upload nothing.
- **Works with no model at all.** Keyword-only mode indexes and searches with
  zero embedding dependencies, so you can start on a machine with no GPU, no API
  key, and no Ollama.
- **Documents, not just code.** PDF, DOCX, XLSX and HTML are extracted to text
  and indexed alongside source files.
- **MCP + agent skills.** A thin MCP server exposes search (with best-effort
  stale flags on hits) and structural code-intelligence tools to AI assistants;
  bundled skills (`semantic-search`, `code-intel`, `impact-before-refactor`, …)
  teach them when to search vs. walk the dependency graph before a refactor.

For the full design, see [docs/architecture.md](docs/architecture.md).

## Model modes and stores

semidx separates *how it embeds* from *where it stores*.

| Model mode | How to select it | What it needs | Notes |
|---|---|---|---|
| Local (Ollama) | default fallback; `--privacy` forces it | a running Ollama with an embedding model | Fully offline. Always the last link in the chain. |
| External API | set a provider key (`GEMINI_API_KEY`, `GROQ_API_KEY`, `OPENROUTER_API_KEY`, `OLLAMA_CLOUD_API_KEY`) or `EMBED_PROVIDER=openai` | network + an API key | Fastest; sensitive files still routed local/text-only. |
| None (keyword) | `--keyword` flag or `SEMIDX_EMBED_MODE=none` | nothing | No embeddings; literal keyword matching only. |

| Store | How to select it | Backend | Best for |
|---|---|---|---|
| PostgreSQL / pgvector | default; used by the server and by `semidx index` | pgvector with per-dimension HNSW indexes | The shared server, large corpora. |
| SQLite (local) | `--local` flag or `SEMIDX_LOCAL_INDEX` | a single SQLite file, brute-force cosine in Go | A single machine / one repo, no server. |

## Install

Prebuilt binaries for **linux / macOS / windows × amd64 / arm64** are attached to
each release as `tar.gz` (`zip` on Windows) with SHA-256 `checksums.txt`.

### Universal installers (recommended)

**Unix (Linux / macOS):**

```sh
curl -fsSL https://raw.githubusercontent.com/lgldsilva/semidx/main/install.sh | sh
```

**Windows (PowerShell):**

```powershell
irm https://raw.githubusercontent.com/lgldsilva/semidx/main/install.ps1 | iex
```

Options (both installers):

```sh
./install.sh --version v0.43.1
./install.sh --os windows --arch arm64 --dest ./dl
./install.sh --all --dest ./dist
```

```powershell
./install.ps1 -Version v0.43.1
./install.ps1 -Destination .\dl -NoInstall
./install.ps1 -All -Destination .\dist
```

Or build from source with Go:

```sh
go install github.com/lgldsilva/semidx/cmd/semidx@latest
```

### Platform package managers

| Channel | Status | Install / update |
|---|---|---|
| GitHub Releases | Live | `install.sh` / `install.ps1`; update with `semidx upgrade` |
| Docker / GHCR | Live | `docker pull ghcr.io/lgldsilva/semidx:latest` |
| Go | Live | `go install github.com/lgldsilva/semidx/cmd/semidx@latest` |
| Homebrew / Scoop | Seeded | Tap/bucket repos exist for v0.44.9; smoke tests pending before live |
| AUR (`yay` / `paru`) | Prepared | Verified PKGBUILD template; publication requires the AUR maintainer account |
| Snap | Prepared | Verified manifest; publication requires Snap Store registration and classic-confinement review |
| winget / Chocolatey | Planned | Upstream repository/review still required |
| Flatpak | Not supported | A sandboxed desktop format is not appropriate for the CLI/server; see `packaging/README.md` |

> Package-manager channels stay off the quickstart until a clean-machine
> install/update smoke test passes. Track rollout in
> [docs/install.md](docs/install.md) and
> [Packaging status #16](https://github.com/lgldsilva/semidx/issues/16).

### Private / self-hosted release mirrors

Point the installers and `semidx upgrade` at a private host with env overrides:

```sh
export SEMIDX_API=https://gitea.example.com/api/v1/repos/owner/semidx
export SEMIDX_DOWNLOAD_BASE=https://gitea.example.com/owner/semidx/releases/download
export SEMIDX_UPDATE_API=$SEMIDX_API
export SEMIDX_UPDATE_URL=$SEMIDX_DOWNLOAD_BASE
# optional token for private release hosts:
export SEMIDX_UPDATE_TOKEN=…
```

If your private host needs basic auth for the raw installer script:

```sh
curl -k -fsSL -u "username:token" https://gitea.example.com/owner/repo/raw/branch/main/install.sh | \
  CURL="curl -k -fsSL -u username:token" SEMIDX_INSECURE=1 sh
```

If you installed via a package manager, `semidx upgrade` detects that and
refuses to overwrite the managed binary — use the package manager’s upgrade
command instead.

## Quickstart (server)

Bring up the server and its database (the database is **not** published to the
host — only the API is):

```bash
docker compose -f deploy/docker-compose.yml up -d --build
```

On first start with an empty database, the server prints a one-time bootstrap
admin token to its logs. Grab it:

```bash
docker compose -f deploy/docker-compose.yml logs semidx | grep "bootstrap admin token"
```

(Alternatively, set `SEMIDX_BOOTSTRAP_TOKEN` before the first start to choose
the token yourself.)

Log in from any machine and register a git repository — the server clones and
indexes it, so nothing is uploaded from the client:

```bash
semidx login https://semidx.example.com --token semidx_xxxxxxxx
semidx repo add https://github.com/acme/myapp.git --name myapp
```

`repo add` queues a full index job and prints the job id. Once it finishes,
search it:

```bash
semidx sgrep --project myapp --query "verify auth token"
```

`sgrep` prints classic `file:line:content` lines. Use `search` for ranked
results with a relevance score, and add `--json` to either for machine-readable
output.

## Quickstart (standalone, no server)

No server, no Postgres — index a directory into a local SQLite file and search
it. `--local` puts the index under your data dir (e.g.
`~/.local/share/semidx/index.db`):

```bash
semidx --local index --project .
semidx --local sgrep --project . --query "how are sessions expired"
```

The project name defaults to the directory's base name, so indexing
`--project ./myapp` creates a project called `myapp` you then search with
`--project myapp`.

### No embedding model? Use keyword mode

`--keyword` indexes and searches with no embedding provider at all — nothing to
install, no API key:

```bash
semidx --local --keyword index --project .
semidx --local --keyword sgrep --project . --query "database migration"
```

### Local embeddings with Ollama

If you have [Ollama](https://ollama.com) running with an embedding model
(e.g. `bge-m3`), semidx uses it automatically as the local provider — point at a
non-default host with `SEMIDX_OLLAMA_URL`:

```bash
export SEMIDX_OLLAMA_URL=http://localhost:11434
semidx --local index --project . --model bge-m3
```

### External embedding provider

Set the key for the provider you want and semidx prepends it to the chain
(cloud providers are skipped for sensitive files):

```bash
export GEMINI_API_KEY=...      # Gemini's OpenAI-compatible endpoint
semidx --local index --project . --model gemini-embedding-2
```

`GROQ_API_KEY`, `OPENROUTER_API_KEY` and `OLLAMA_CLOUD_API_KEY` are wired the
same way. For any other OpenAI-compatible service, set `EMBED_PROVIDER=openai`,
`EMBED_ENDPOINT=https://…/v1` and `EMBED_API_KEY=…`. See
[docs/self-hosting.md](docs/self-hosting.md) for the full list.

## MCP setup (AI agents)

The `mcp` subcommand runs a thin MCP server over stdio — standalone over the
local index, or proxying the server you logged in to. Core tools accept
**project names** (not arbitrary filesystem paths for reindex), so an agent
cannot index an unregistered directory through the server path.

**Search & project tools** (work in standalone and remote modes):

| Tool | Purpose |
|---|---|
| `semantic_search` | Natural-language code search; hits may include best-effort `stale` + `indexed_at` when the on-disk file no longer matches the indexed hash |
| `semantic_status` | Indexing status for a registered project (file count, status, model) |
| `semantic_projects` | List registered projects and their status |
| `semantic_reindex` | Queue a re-index on the **server** (standalone mode returns an in-band tip to run `semidx index`) |
| `semantic_ask` | RAG-augmented Q&A over indexed code with cited chunks |

**Code-intelligence tools** (standalone/local index only — remote returns an
in-band "standalone/local mode only" message):

| Tool | Args | Purpose |
|---|---|---|
| `semantic_callers` | `project?`, `file`, `line` | Files that import the package of the symbol at `file:line` |
| `semantic_explain` | `project?`, `file`, `line` | Symbol kind/location, deps, importers, related tests |
| `semantic_impact` | `project?`, `file`, `line`, `depth?` (default 5, max 10) | Blast radius: transitive reverse deps (**MCP-only**; no CLI yet) |
| `semantic_deadcode` | `project?` | Unused symbols (`confirmed` safe-to-delete / `public-api` review) |
| `semantic_diff` | `ref_range` (`ref1..ref2` or `ref1...ref2`) | New/removed/changed-signature symbols between two git refs |

CLI equivalents (local): `semidx callers <file:line>`, `semidx explain <file:line>`,
`semidx dead-code`, `semidx diff <ref1>..<ref2>`.

Log in once (for remote), then register the MCP server with your agent. Example
Claude Code / MCP client config entry:

```json
{
  "mcpServers": {
    "semidx": {
      "command": "semidx",
      "args": ["mcp"]
    }
  }
}
```

Install the bundled agent skills into `~/.claude/skills` (includes
`semantic-search`, `code-intel`, `impact-before-refactor`, `auto-index`, and
`workspace-agent` — when to search vs. walk structure before a refactor):

```bash
semidx skills install
```

Use `--target project` to install into `./.claude/skills`, or `--dir <path>` for
an explicit location.

## Web admin

`semidx serve` mounts a server-rendered management UI at `/admin`
(`https://semidx.example.com/admin`). To create the first admin user, set a
password before the first start:

```bash
export SEMIDX_BOOTSTRAP_ADMIN_USER=admin       # default: admin
export SEMIDX_BOOTSTRAP_ADMIN_PASSWORD='choose-a-strong-one'
```

From the UI you can browse and search projects, manage users, and issue two
kinds of credentials:

- **API keys** — opaque bearer tokens (`semidx_…`), shown once, used by the CLI
  and SDK. Only their hash is stored; revoke any time.
- **JWT control tokens** — revocable HS256 tokens for UI-driven access, enabled
  by setting `SEMIDX_JWT_SECRET`. Each carries a unique id so it can be revoked
  even if it never expires.

See [docs/self-hosting.md](docs/self-hosting.md) to run the server and
[docs/api.md](docs/api.md) for the REST surface.

## Documentation

- [docs/install.md](docs/install.md) — full install/update matrix and roll-out status.
- [docs/architecture.md](docs/architecture.md) — how it is built.
- [docs/requirements.md](docs/requirements.md) — product requirements and backlog status.
- [docs/self-hosting.md](docs/self-hosting.md) — running the server.
- [docs/api.md](docs/api.md) — the REST v1 API.
- [SECURITY.md](SECURITY.md) — the security model and how to report issues.
- [CONTRIBUTING.md](CONTRIBUTING.md) — development setup and ground rules.

## License

Apache License 2.0. See [LICENSE](LICENSE).
