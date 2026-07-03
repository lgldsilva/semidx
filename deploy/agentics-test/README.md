# Agentics MCP test harness

Proves, end to end, that **`semidx mcp install` wires real coding agents to
semidx** and that the **semidx MCP server actually speaks the protocol** — across
every storage backend. It is the integration counterpart to the unit tests in
`internal/mcpinstall` and `internal/mcpserver`.

## What it checks

For each variation the harness runs:

1. **Client wiring gate** — `semidx mcp install --apply` for every supported
   client (Claude Code, Claude Desktop, Cursor, Windsurf, Gemini CLI, Antigravity,
   GitHub Copilot, VS Code, OpenCode, Crush) and asserts the written config matches
   that agent's schema; Codex and cagent are verified as print-only; a merge test
   proves an existing server is preserved and a `.bak` is written. See the full
   list with `semidx mcp install --list`.
2. **Skill install** — `semidx skills install` produces `semantic-search/SKILL.md`.
3. **Live MCP handshake** — `mcp-probe` spawns the exact `semidx mcp` command an
   agent would launch, performs `initialize` + `tools/list`, and calls
   `semantic_projects` / `semantic_search` over real stdio.
4. **Native agent check** (best-effort) — if `claude` is installed,
   `claude mcp list` must report the `semidx` server.
5. **Real agent tool-call** (server variation, gated) — if `ANTHROPIC_API_KEY`
   is set, a real Claude turn must use `semantic_search` and find the hit.

## Variations

| Variation    | Postgres | Model      | Server | What it proves |
|--------------|----------|------------|--------|----------------|
| `keyword`    | no       | none       | no     | Fully hermetic: MCP works standalone with zero dependencies |
| `standalone` | no       | Ollama/API | no     | MCP works standalone over local SQLite with semantic search (SKIPs if no provider) |
| `server`     | yes      | Ollama/API | yes    | MCP works against a running server sharing pgvector; optional real-agent call |

## Run it

No-Postgres variations (single container, always hermetic for `keyword`):

```bash
# from the repo root
docker build -f deploy/agentics-test/Dockerfile.agents -t semidx-agents .
docker run --rm semidx-agents keyword      # hermetic, no deps
docker run --rm semidx-agents all          # keyword + standalone
```

With-server variation (full stack, no host ports published):

```bash
docker compose -f deploy/agentics-test/docker-compose.yml up --build \
  --abort-on-container-exit --exit-code-from agents

# with a real agent call:
ANTHROPIC_API_KEY=sk-... docker compose -f deploy/agentics-test/docker-compose.yml \
  up --build --abort-on-container-exit --exit-code-from agents
```

Run the scripts directly (outside Docker) against locally built binaries:

```bash
go build -o /tmp/bin/semidx ./cmd/semidx
go build -o /tmp/bin/mcp-probe ./deploy/agentics-test/mcp-probe
PATH=/tmp/bin:$PATH ./deploy/agentics-test/run.sh all
```

The harness exits non-zero if any hard gate fails; `SKIP`s (no model, no API
key, absent CLI) are non-blocking.

## Files

- `run.sh` — orchestrator and variation logic
- `lib.sh` — assertions (config schema checks, merge/backup, skill install, summary)
- `mcp-probe/` — a tiny Go MCP client that drives a real stdio handshake
- `Dockerfile.agents` — semidx + probe + agent CLIs + the harness
- `docker-compose.yml` — the `server` variation stack
- `testdata/fixture/` — a small code fixture to index and search (under `testdata/`
  so the Go toolchain ignores it)
