# Usage analytics & doctor

Product-level search analytics (inspired by [ai-memory](https://github.com/akitaonrails/ai-memory)
`auto-improve-report` / install inventory), not SaaS telemetry.

## Quick commands

```bash
semidx doctor          # MCP configs + skills + active backend
semidx usage           # last 30d aggregates + findings (includes p50/p95 latency)
semidx usage --days 7 --json
semidx usage --project jackui
semidx skills install --all   # refresh auto-index + other bundled skills on every agent
semidx mcp install --client claude-code --apply
```

Admin UI: **Usage** nav. MCP: `semantic_usage` (when backend supports it).
API: `GET /api/v1/search-usage?days=30` (not to be confused with
`GET /api/v1/usage`, which reports tenant billing quota).

## Adoption checklist (raise real usage)

1. **TLS hostname** — `semidx login` must use a host covered by the server certificate
   (e.g. `semidx.internal.example.com`, not a `.lan` name missing from the SAN).
2. **CLI current** — `semidx doctor` / `semidx usage` require a recent binary.
3. **MCP + skills** — wire MCP (`semidx mcp install --all --apply`) and install
   skills (`semidx skills install --all`), especially `auto-index` (session-start routing).
4. **Default project (remote)** — set `default_project` on login or
   `SEMIDX_DEFAULT_PROJECT` so agents can omit `project`. Standalone MCP resolves
   the indexed repo from cwd when possible.
5. **Attribution** — CLI/MCP send `X-Semidx-Client`; `semidx usage` should show
   `mcp`/`cli` instead of `unknown`.

## What is recorded

| Field | Notes |
|---|---|
| project | Resolved project name |
| source | `cli` / `mcp` / `admin` / `sdk` / `unknown` |
| outcome | `ok` / `empty` / `fallback` / `error` |
| hit_count, latency_ms, keyword, graph | latency rolls up as p50/p95 in the report |
| query_hash | SHA-256; text only if `SEMIDX_USAGE_LOG_QUERIES=true` |

Prometheus also exposes `semidx_search_total{project,source,outcome}`.

## Privacy

- No outbound phone-home.
- Query text off by default.
- Git URLs in listings are redacted (userinfo stripped).
- OTEL spans no longer attach the raw query string.

## Phase-0 snapshot (homelab, 2026-07-24)

- Active path: **remote** server; MCP on Cursor/Codex/OpenCode/Gemini; Claude Code
  MCP often missing — `doctor` flags it.
- Local SQLite index may exist as residual test data without useful embeddings.
- Many `semidx-*` fixture projects can clutter `semantic_projects` — drop unused.
- Pre-feature Prometheus only had successful-search histograms (no source/outcome).
