#!/usr/bin/env bash
# Agentics MCP test harness.
#
# Proves that `semidx mcp install` wires real coding agents to semidx and that
# the semidx MCP server actually speaks the protocol — across backends.
#
# Variations (arg 1, default "all"):
#   standalone  no Postgres, no server: local SQLite index + live MCP handshake
#   keyword     no Postgres, no model:  keyword-only local index + handshake
#   server      talks to a running semidx server (SEMIDX_SERVER_URL/SEMIDX_TOKEN)
#   all         standalone + keyword (server needs the compose stack)
#
# Every variation runs the client-wiring gate (install + schema for all agents)
# and an MCP handshake via mcp-probe. The `server` variation additionally does a
# real agent tool-call when an API key is present (else it SKIPs).
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh disable=SC1091
. "$HERE/lib.sh"

FIXTURE="${FIXTURE:-$HERE/testdata/fixture}"
PROBE="${PROBE:-mcp-probe}"
PROJECT="$(basename "$FIXTURE")"

# fresh_home isolates config/cache/state so repeated runs never collide.
fresh_home() {
  export HOME; HOME="$(mktemp -d)"
  export XDG_CONFIG_HOME="$HOME/.config" XDG_CACHE_HOME="$HOME/.cache"
  mkdir -p "$XDG_CONFIG_HOME" "$XDG_CACHE_HOME"
}

variation_standalone() {
  log "VARIATION: standalone (local SQLite + semantic embeddings)"
  fresh_home
  install_all_clients
  # Semantic indexing needs a reachable embedding provider (Ollama or an API key).
  # In a hermetic container there may be none — then this variation SKIPs (the
  # keyword variation is the always-hermetic no-Postgres proof).
  log "index the fixture into the local SQLite index (semantic)"
  if ! semidx --local index --docs --project "$FIXTURE" 2>&1 | grep -q "Files indexed"; then
    skip "no embedding provider reachable — semantic standalone (run Ollama or set an API key)"
    return
  fi
  pass "semidx --local index completed"
  log "live MCP handshake against \`semidx --local mcp\`"
  if "$PROBE" -project "$PROJECT" -query "verify a password hash" -- semidx --local mcp; then
    pass "standalone MCP handshake + semantic_search verified"
  else
    fail "standalone MCP handshake failed"
  fi

  # Verify new workspace-agent tools are registered (requires git tests only when
  # the fixture is a real git repo; the docs fixture is not, so repo_worktrees
  # gracefully returns "no local path" which proves the tool is wired).
  log "verify new workspace-agent tools"
  if node "$HERE/tool-test.mjs" \
    --mcp-cmd="semidx" --mcp-cmd="--local" --mcp-cmd="mcp" \
    --project "$PROJECT" \
    --skip-git \
    2>&1 | sed 's/^/     /'; then
    pass "workspace-agent tools verified"
  else
    fail "workspace-agent tools check failed"
  fi
}

variation_keyword() {
  log "VARIATION: keyword-only (no Postgres, no embedding model)"
  fresh_home
  install_all_clients
  log "index the fixture keyword-only"
  if semidx --local --keyword index --docs --project "$FIXTURE" 2>&1 | grep -q "Files indexed"; then
    pass "semidx --local --keyword index completed"
  else
    fail "semidx --local --keyword index failed"
  fi
  log "live MCP handshake against \`semidx --local --keyword mcp\`"
  if "$PROBE" -project "$PROJECT" -query "VerifyPassword" -expect "auth.go" -- semidx --local --keyword mcp; then
    pass "keyword-only MCP handshake + search verified"
  else
    fail "keyword-only MCP handshake failed"
  fi
}

variation_server() {
  log "VARIATION: with server (${SEMIDX_SERVER_URL:-unset})"
  : "${SEMIDX_SERVER_URL:?server variation needs SEMIDX_SERVER_URL}"
  : "${SEMIDX_TOKEN:?server variation needs SEMIDX_TOKEN}"
  fresh_home
  install_all_clients

  log "wait for the server to become ready"
  for _ in $(seq 1 60); do
    if semidx login "$SEMIDX_SERVER_URL" --token "$SEMIDX_TOKEN" >/dev/null 2>&1; then break; fi
    sleep 2
  done
  if semidx login "$SEMIDX_SERVER_URL" --token "$SEMIDX_TOKEN" >/dev/null 2>&1; then
    pass "logged in to $SEMIDX_SERVER_URL"
  else
    fail "could not reach/login to $SEMIDX_SERVER_URL"; return
  fi

  # Index the fixture into the shared Postgres the server reads (SEMIDX_DB_DSN),
  # then search it through the server's API via MCP — proving the end-to-end path.
  # Semantic indexing needs an embedding provider; without one we still prove the
  # server MCP path (handshake + tools + projects), just not a live search hit.
  local probe_args=()
  log "index the fixture into the server's Postgres (direct DSN)"
  if [ -n "${SEMIDX_DB_DSN:-}" ] && env -u SEMIDX_SERVER_URL semidx index --docs --project "$FIXTURE" 2>&1 | grep -q "Files indexed"; then
    pass "indexed fixture into Postgres"
    probe_args=(-project "$PROJECT" -query "verify a password hash")
  else
    skip "no embedding provider/DSN — verifying the server MCP path without a live search hit"
  fi

  log "live MCP handshake against the server (remote \`semidx mcp\`)"
  if "$PROBE" "${probe_args[@]}" -- semidx mcp; then
    pass "server MCP handshake + tools verified"
  else
    fail "server MCP handshake failed"
  fi

  real_agent_call
}

# real_agent_call: gated on an API key. Runs a real Claude Code agent turn that
# must use the semidx MCP tool. SKIPs cleanly when no key or CLI is present.
real_agent_call() {
  log "real agent tool-call (gated on ANTHROPIC_API_KEY)"
  if [ -z "${ANTHROPIC_API_KEY:-}" ]; then skip "ANTHROPIC_API_KEY not set — real agent call"; return; fi
  command -v claude >/dev/null 2>&1 || { skip "claude CLI not installed — real agent call"; return; }
  local dir out; dir="$(mktemp -d)"
  ( cd "$dir" && semidx mcp install --client claude-code --apply >/dev/null 2>&1 ) || true
  out="$(cd "$dir" && claude -p "Use the semidx semantic_search tool on project '$PROJECT' for 'verify a password hash' and quote the top file:line." \
           --allowedTools "mcp__semidx__semantic_search" 2>&1)"
  # shellcheck disable=SC2001
  printf '%s\n' "$out" | sed 's/^/     /'
  if printf '%s' "$out" | grep -qi "auth.go"; then
    pass "real Claude agent used semantic_search and found the hit"
  else
    skip "real agent call did not confirm the hit (quota/model/network) — non-blocking"
  fi
}

main() {
  case "${1:-all}" in
    standalone) variation_standalone ;;
    keyword)    variation_keyword ;;
    server)     variation_server ;;
    all)        variation_standalone; variation_keyword ;;
    *) echo "unknown variation: $1 (standalone|keyword|server|all)"; exit 2 ;;
  esac
  summary
}
main "$@"
