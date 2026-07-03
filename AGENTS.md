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
  - Documents (`--docs`) are keyed by absolute path. Extractors cover text/code,
    Markdown/HTML, PDF, Word, Excel, and `.class` inside JARs (structural
    baseline + optional external decompiler via `SEMIDX_JAVA_DECOMPILER`).
- **Search** semantically, with an automatic, **explicit** keyword fallback when
  embeddings are unavailable (`fallback: true`), plus a no-embeddings
  keyword-only mode (`--keyword`).
- **Embedding chain** with privacy routing: Gemini → Groq → OpenRouter → Ollama
  Cloud → local Ollama (fallback). Sensitive files are routed local or stored
  text-only.
- **Three storage backends** (see below): PostgreSQL/pgvector, local SQLite, or a
  remote server.
- **MCP** server over stdio (standalone over the local index, or proxying a
  server) exposing `semantic_search`, `semantic_projects`, `semantic_reindex`.
- **`semidx mcp install`** wires the MCP server into 12 agent clients
  (Claude Code/Desktop, Cursor, Windsurf, Gemini CLI, Antigravity, GitHub
  Copilot, VS Code, OpenCode, Crush; Codex + cagent are print-only).
- **`semidx config`** persists provider keys + the backend choice
  (`~/.config/semidx/semidx.env`); `semidx skills install` ships agent skills.
- **Web admin** (`/admin`, embedded via `embed.FS`): users, API keys, JWT control
  tokens, projects; argon2id, CSRF, server-side sessions.

## What it does NOT do

- **Not a SaaS and not a grep replacement.** Use plain grep/ripgrep for exact
  strings; semidx is for intent/behavior queries.
- **The CLI does not push an index to the server over the API.** `semidx index`
  writes to a store it reaches **directly** — local SQLite or Postgres
  (`SEMIDX_DB_DSN`). Server-side indexing is for **git** sources the server
  clones itself. (A `push` project + files/diff+batch API exists on the server,
  but the CLI's `index` path is direct-to-store.)
- **MCP `semantic_reindex` is server-only.** In standalone mode it returns an
  in-band message pointing at `semidx index`.
- **No PR/branch SonarQube analysis** — the homelab Sonar is **Community
  edition** (single branch). Sonar runs on **main only**.
- **Postgres is never exposed** — the only ingress is the authenticated HTTP API.
- **Ollama is not bundled**; the host provides it (GPU box). Without any provider,
  use `--keyword`.
- **Deploy is pull-based** (Watchtower) — CI never SSHes into a host.

## Architecture & layout

```
cmd/semidx/        CLI (cobra): index, search, sgrep, config, mcp[/install],
                   skills, models, repo, login, drop, serve; main.go wires deps.
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
deploy/            docker-compose (self-host), homelab/ (Watchtower deploy),
                   agentics-test/ (the MCP integration harness)
docs/              architecture.md, api.md, self-hosting.md, CICD.md, ADRs
```

## Storage backends (pick one; runtime precedence: remote > SQLite > Postgres)

| Backend | Select with | Notes |
|---|---|---|
| Remote server | `semidx login <url> --token …` | CLI/MCP proxy the HTTP API |
| Local SQLite  | `--local` / `SEMIDX_LOCAL_INDEX` | one file, brute-force cosine |
| Postgres      | `SEMIDX_DB_DSN` (or the compose default) | pgvector + HNSW; the server's store |

`semidx config list` reports the active backend.

## Build, test & quality gates

Pin the toolchain — several tools need it and `@latest` may require newer Go:

```sh
export GOTOOLCHAIN=go1.25.11
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

## CI/CD (Gitea Actions)

- **`ci.yml` = the gates**, on every PR and push to main: build, `go test -race`,
  golangci-lint, gitleaks, govulncheck, gosec, Trivy image scan. **These predict
  what may enter main.** SonarQube runs **on main only** (Community edition).
- **`release.yml` = deploy + publish**, on `v*` tags / dispatch: build+push the
  image, SBOM → Dependency-Track, **GoReleaser** artifacts
  (linux/darwin/windows × amd64/arm64 tar.gz/zip + SHA-256 + changelog),
  then **deploy** by triggering the host's **Watchtower** to pull `:latest`.
- Every infra step is **gated on its secret** and skips cleanly when absent, so
  secret-less runs stay green. Secrets/vars: `SONAR_TOKEN`, `REGISTRY_TOKEN`,
  `GITEA_TOKEN`, `WATCHTOWER_URL`+`WATCHTOWER_TOKEN`, `SONAR_HOST_IP`, … (see
  `docs/CICD.md`).
- goreleaser is pinned to **v2.13.0** and gosec to **v2.27.1** because `@latest`
  needs Go ≥1.26 which `GOTOOLCHAIN=local` blocks.

> **KNOWN ISSUE:** pushes to `main` currently do **not** trigger CI on this Gitea
> instance (only PR/branch events do). So the main-only Sonar gate does not run
> automatically on merge, and releases must be confirmed via the tag event. This
> is a Gitea-side trigger/config problem, not a workflow bug.

## Conventions

- **Conventional Commits.** The commit-msg hook **rejects a comma in the scope**
  (use one scope, e.g. `fix(mcp)` not `fix(mcp,skills)`) and warns on a subject
  over 72 chars. End commit messages with a `Claude-Session:` trailer.
- **Never commit directly to `main` — always a PR.** Gitea has no `gh`; open/merge
  PRs via the REST API (`gitea-pr` skill). Branch from an **updated** main.
- After an API merge, the `refs/heads/main` transport ref lags a few seconds —
  wait for it before relying on the remote head.
- **Secrets hygiene:** no keys/DSNs in code or committed files; providers/keys via
  `semidx config` or env. `.env` files and `deploy/**/.env` are gitignored.
- Validate end-to-end before declaring done (build/tests/curl/logs), and test
  with **real inputs** (varied/password-protected/corrupt files), not just
  synthetic unit tests.

## Gotchas (hard-won)

- `GOTOOLCHAIN=go1.25.11` is deliberate (stdlib CVEs fixed vs. 1.25.7). Bump the
  `golang:` builder image too when bumping — the stdlib ships in the binary.
- SQLite local store: `journal_mode=WAL`, `busy_timeout`, `MaxOpenConns(1)` —
  serialises writers, avoids "database is locked" and corruption. Never mix
  journal modes across processes.
- The worktree filter applies to **git** projects only; document/push projects
  ignore it (else searching them from inside an unrelated repo returns nothing).
- MCP stdio: keep the server's stdout for the protocol (logs go to stderr).

## Docs

`docs/architecture.md` · `docs/api.md` · `docs/self-hosting.md` ·
`docs/CICD.md` · `docs/design-decisions.md` (ADRs) · `README.md` (quickstart) ·
`deploy/agentics-test/README.md` (MCP harness).
