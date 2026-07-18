# AGENTS.md — semidx

Canonical guide for AI coding agents (Claude Code, Codex, Gemini CLI, OpenCode,
Cursor, Crush, cagent, …) working in this repository. Any CLI that reads
`AGENTS.md` gets the same context; `CLAUDE.md` just points here.

> This is **project** context. It does not override a machine's personal/global
> standards (e.g. `~/.config/ai-standards/AGENTS.md`) — it complements them with
> what is specific to semidx.

---

## What semidx is

**semidx** is self-hosted **semantic code search**: it chunks code/docs, embeds
the chunks, stores the vectors, and answers natural-language queries with ranked
`file:line` matches. It is **client–server** but also fully **standalone**:

- One Go binary (`semidx`) with a `serve` subcommand (the HTTP API + web admin).
- The **CLI is the primary interface**; the **MCP server is a thin client** that
  exposes search to AI agents.
- Module `github.com/lgldsilva/semidx`, env prefix `SEMIDX_*`, Apache-2.0.

Origin: a homelab PoC (`poc-semantic-indexer`) hardened into an OSS product.

## What it does

- **Index** git repos, plain document folders, and archives (JAR/WAR/AAR).
  - Git projects are one logical index keyed by **repo identity**; content is
    **addressed by hash**, so the same file across worktrees embeds once, and
    each worktree searches the version it has checked out.
  - Documents (`--docs`) are keyed by absolute path. Extractors (pluggable
    registry) cover: code + text/config (`.csv/.tsv/.log/.ini/.cfg/.conf`),
    Markdown/HTML, PDF, Word/Excel/PowerPoint (`.docx/.xlsx/.pptx`), OpenDocument
    (`.odt/.ods/.odp`), EPUB, Jupyter (`.ipynb`, cell-aware), RTF, and `.class`
    inside JARs (baseline + optional decompiler via `SEMIDX_JAVA_DECOMPILER`).
    Not yet covered: legacy Office (`.doc/.xls/.ppt`), email, images/OCR, generic
    archives (`.zip/.tar`).
- **Search** semantically, with an automatic, **explicit** keyword fallback when
  embeddings are unavailable (`fallback: true`), plus a no-embeddings
  keyword-only mode (`--keyword`). `--project` accepts a **path** (resolved by
  unique identity, so same-basename folders never collide) or a name; with no
  `--project`, search resolves the project enclosing the current directory, and
  falls back to searching **all** projects (labeled per project).
- **Embedding chain** with privacy routing: Gemini → Groq → OpenRouter → Ollama
  Cloud → local Ollama (fallback). Sensitive files are routed local or stored
  text-only.
- **Three storage backends** (see below): PostgreSQL/pgvector, local SQLite, or a
  remote server.
- **MCP** server over stdio (standalone over the local index, or proxying a
  server) exposing `semantic_search`, `semantic_projects`, `semantic_reindex`.
- **`semidx mcp install`** wires the MCP server into 12 agent clients
  (Claude Code/Desktop, Cursor, Windsurf, Gemini CLI, Antigravity, GitHub
  Copilot, VS Code, OpenCode, Crush, Codex; **cagent** is print-only (YAML toolset has no safe merge)).
- **`semidx config`** persists provider keys + the backend choice
  (`~/.config/semidx/semidx.env`); `semidx skills install` ships agent skills.
- **Web admin** (`/admin`, embedded via `embed.FS`): users, API keys, JWT control
  tokens, projects; argon2id, CSRF, server-side sessions.

## What it does NOT do

- **Not a SaaS and not a grep replacement.** Use plain grep/ripgrep for exact
  strings; semidx is for intent/behavior queries.
- **The CLI `index` command writes directly to a local store**, not through the
  server API. Use `semidx push` to send local files to a server for remote
  indexing — in raw mode (server chunks+embeds) or `--embed-locally` mode
  (client chunks+embeds locally, server stores). `semidx repo add` handles
  server-side git cloning.
- **MCP `semantic_reindex` is server-only.** In standalone mode it returns an
  in-band message pointing at `semidx index`.
- **No PR/branch SonarCloud analysis by default** — SonarCloud runs on `main`
  (and PRs when `SONAR_TOKEN` is configured). Homelab SonarQube is retired for
  this repo.
- **Postgres is never exposed** — the only ingress is the authenticated HTTP API.
- **Ollama is not bundled**; the host provides it (GPU box). Without any provider,
  use `--keyword`.
- **Deploy is pull-based** (Watchtower) — CI never SSHes into a host.

## Architecture & layout

```
cmd/semidx/        CLI (cobra): index, unlock (password docs), search, sgrep,
                   migrate (SQLite→Postgres), config, mcp[/install], skills,
                   models, repo, login, drop, serve; main.go wires deps.
pkg/client/        public Go SDK for the HTTP API (DTOs + client).
internal/
  config/          SEMIDX_* resolution: env > cwd .env > ~/.config/semidx/semidx.env > default
  clientconfig/    ~/.config/semidx/config.yaml (server_url, token, default project)
  chunker/         line-aware chunking (Chunk{Content,StartLine,EndLine})
  privacy/         deny-list + route-local policy engine
  embed/           Embedder + ChainEmbedder (privacy is a parameter)
  extract/         pluggable extractor registry (text/pdf/docx/xlsx/…, JAR)
  store/           PgStore + goose migrations; dynamic chunks_<dims> + HNSW
  localstore/      standalone SQLite (WAL, MaxOpenConns=1, identity-isolated)
  indexing/        walk + hash-diff + worktree manifest; content-addressed
  gitmeta/         repo identity + worktree toplevel (exec git)
  gitsync/         server-side clone/pull
  search/          one Service (vector + keyword fallback, worktree filter)
  server/ webadmin/ jwtauth/ passwd/   HTTP API + admin UI + auth
  mcpserver/       MCP tools over a Backend (remote client | local index)
  mcpinstall/      `mcp install` client registry (per-client config formats)
  skills/          embedded agent skills
deploy/            docker-compose (self-host),
                   agentics-test/ (the MCP integration harness)
docs/              architecture.md, api.md, self-hosting.md, CICD.md, ADRs
```

## Storage backends (pick one; runtime precedence: remote > Postgres (configured) > SQLite > Postgres (default))

| Backend | Select with | Notes |
|---|---|---|
| Remote server | `semidx login <url> --token …` | CLI/MCP proxy the HTTP API |
| Local SQLite  | `--local` / `SEMIDX_LOCAL_INDEX` | one file, brute-force cosine |
| Postgres      | `SEMIDX_DB_DSN` (or the compose default) | pgvector + HNSW; the server's store |

`semidx config list` reports the active backend.

## Build, test & quality gates

Pin the toolchain — several tools need it and `@latest` may require newer Go:

```sh
export GOTOOLCHAIN=go1.26.5
go build ./...
go test -race -shuffle=on ./...          # testcontainers tests skip w/o Docker
gofmt -l .                               # must be empty
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./...
go run github.com/securego/gosec/v2/cmd/gosec@v2.27.1 -quiet ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

- Coverage target **≥90%** on business packages (`internal/**`, `pkg/**`);
  `cmd/**` and `deploy/**` are excluded from the Sonar coverage denominator.
- Non-buildable fixtures live under `testdata/` (Go + gosec ignore it).
- Suppress a security finding only with a justified `// #nosec RULE -- reason`.
- Run the MCP integration harness: `deploy/agentics-test/run.sh {keyword|standalone|all}`
  (server variation via its `docker-compose.yml`). See its `README.md`.
- Local Sonar gate (needs a token): `SONAR_TOKEN=… ./scripts/sonar-scan.sh`.

## CI/CD (GitHub Actions)

- **`ci.yml` = the gates**, on every PR and push to `main`: build, `go test -race`,
  golangci-lint, gitleaks, govulncheck, gosec, Trivy fs scan. **These predict
  what may enter `main`.**
- **`release.yml` = publish**, on `v*` tags / dispatch: **GoReleaser** artifacts
  (linux/darwin/windows × amd64/arm64 tar.gz/zip + SHA-256) to a **GitHub
  Release**, plus Docker image to **ghcr.io/lgldsilva/semidx**.
- **`autotag.yml`** keeps version tags in sync via
  [`svu`](https://github.com/caarlos0/svu) (next semver from Conventional Commits).
- **Code scanning** uses GitHub's default CodeQL setup (not a workflow file) —
  do not re-add `.github/workflows/codeql.yml` while default setup is enabled
  (the two conflict and the advanced workflow fails every run).
- **`dependency-review.yml`** blocks PRs that introduce high/critical vulnerable
  dependencies (free for public repos).
- **SonarCloud** runs via the SonarCloud **GitHub App (Automatic Analysis)** —
  no workflow file and no `SONAR_TOKEN` needed. Do NOT add a CI-based
  `sonarcloud-github-action` workflow: it conflicts with Automatic Analysis
  (SonarCloud refuses to run both). Config lives in `sonar-project.properties`.
  Note: Automatic Analysis does not run tests, so Go coverage is not uploaded;
  run `scripts/sonar-scan.sh` locally for a coverage-aware gate if needed.
- **Dependabot** (`dependabot.yml`) opens weekly grouped PRs for Go modules and
  GitHub Actions.
- goreleaser is pinned to **v2.13.0** and gosec to **v2.27.1**.

> **Migration note:** a Gitea mirror may still be alive during cutover. Prefer
> GitHub as the source of truth for PRs/releases. Homelab deploy pulls from
> **ghcr.io** via Watchtower (`deploy/docker-compose.pull.yml`).

## Conventions

- **Conventional Commits.** The commit-msg hook **rejects a comma in the scope**
  (use one scope, e.g. `fix(mcp)` not `fix(mcp,skills)`) and warns on a subject
  over 72 chars. End commit messages with a `Claude-Session:` trailer.
- **Never commit directly to `main` — always a PR.** Branch from an **updated**
  `main` and open a PR; CI gates (above) run on every PR and must be green
  before merge.
- **Secrets hygiene:** no keys/DSNs in code or committed files; providers/keys via
  `semidx config` or env. `.env` files and `deploy/**/.env` are gitignored.
- Validate end-to-end before declaring done (build/tests/curl/logs), and test
  with **real inputs** (varied/password-protected/corrupt files), not just
  synthetic unit tests.

## Gotchas (hard-won)

- `GOTOOLCHAIN=go1.26.5` is deliberate: `charm.land/fantasy` (the chat/agent LLM
  layer) requires Go ≥1.26.5, and 1.26.5 also carries the stdlib CVE fixes. Bump
  the `golang:` builder image (Dockerfile + deploy/agentics-test) together with
  the Makefile/go.mod when bumping — the stdlib ships in the binary.
- SQLite local store: `journal_mode=WAL`, `busy_timeout`, `MaxOpenConns(1)` —
  serialises writers, avoids "database is locked" and corruption. Never mix
  journal modes across processes. **Note:** with `IndexWorkers` > 1, parallel
  file workers still funnel writes through a single DB connection — expect
  diminishing returns on large local indexes; Postgres is the scale path.
- The worktree filter applies to **git** projects only; document/push projects
  ignore it (else searching them from inside an unrelated repo returns nothing).
- MCP stdio: keep the server's stdout for the protocol (logs go to stderr).
- CI: `setup-go` runs with `cache: false` on purpose. semidx's Go cache balloons
  to ~3.5 GB (many `go run …@version` tool builds + `-race` + testcontainers),
  which exceeds the default GitHub Actions cache eviction pressure for this
  repo and intermittently fails whichever job's cache-save loses the race.
  Re-downloading modules each run is slower but reliable. If you re-enable
  the cache, keep an eye on hit rate and quota.

## Docs

`docs/architecture.md` · `docs/requirements.md` (product requirements) ·
`docs/api.md` · `docs/self-hosting.md` ·
`docs/design-decisions.md` (ADRs) ·
`docs/research/large-scale-semantic-code-search.md` (scale research + roadmap) ·
`docs/install.md` (install matrix and CI/private-host setup) ·
`README.md` (quickstart) · `deploy/agentics-test/README.md` (MCP harness).
