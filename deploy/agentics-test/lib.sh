#!/usr/bin/env bash
# Shared assertions for the agentics MCP test harness. Sourced by run.sh.
# Tracks pass/fail/skip counts; a single hard failure makes the run exit non-zero.
set -uo pipefail

PASS=0 FAIL=0 SKIP=0

log()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
pass() { printf '  \033[32mPASS\033[0m %s\n' "$*"; PASS=$((PASS+1)); }
fail() { printf '  \033[31mFAIL\033[0m %s\n' "$*"; FAIL=$((FAIL+1)); }
skip() { printf '  \033[33mSKIP\033[0m %s\n' "$*"; SKIP=$((SKIP+1)); }

# json_check <file> <node-expr>: passes if the file exists and node-expr (given
# the parsed JSON as `c`) is truthy. Uses node (always present — agents are npm).
json_check() {
  local file="$1" expr="$2"
  [ -f "$file" ] || { return 1; }
  node -e '
    const fs=require("fs");
    const c=JSON.parse(fs.readFileSync(process.argv[1],"utf8"));
    process.exit(('"$expr"') ? 0 : 1);
  ' "$file" 2>/dev/null
}

# install_json_client <client> <json-expr>: run `semidx mcp install --apply` for a
# JSON-config client into a temp file, then assert the written entry matches.
install_json_client() {
  local client="$1" expr="$2"
  local out; out="$(mktemp -d)/config.json"
  if ! semidx mcp install --client "$client" --config-file "$out" --apply >/dev/null 2>&1; then
    fail "$client: mcp install --apply failed"
    return
  fi
  if json_check "$out" "$expr"; then
    pass "$client: config written with a valid semidx entry"
  else
    fail "$client: written config missing/invalid semidx entry ($out)"
  fi
}

# install_preserves_others <client> <json-expr>: seed a config with an unrelated
# server, install semidx, and assert BOTH survive (idempotent merge + backup).
install_preserves_others() {
  local client="$1" expr="$2"
  local out; out="$(mktemp -d)/config.json"
  printf '{"mcpServers":{"other":{"command":"x"}},"servers":{"other":{"command":"x"}}}' > "$out"
  semidx mcp install --client "$client" --config-file "$out" --apply >/dev/null 2>&1
  if json_check "$out" "$expr" \
     && node -e 'const c=require(process.argv[1]);process.exit((c.mcpServers?.other||c.servers?.other)?0:1)' "$out" 2>/dev/null \
     && ls "$out".bak-* >/dev/null 2>&1; then
    pass "$client: merge preserved the unrelated server + wrote a backup"
  else
    fail "$client: merge dropped the unrelated server or wrote no backup"
  fi
}

# install_toml_client <client> <grep-pattern>: run `semidx mcp install --apply`
# for a TOML-config client into a temp file, then assert the written entry.
install_toml_client() {
  local client="$1" pat="$2"
  local out; out="$(mktemp -d)/config.toml"
  if ! semidx mcp install --client "$client" --config-file "$out" --apply >/dev/null 2>&1; then
    fail "$client: mcp install --apply failed"
    return
  fi
  if grep -q "$pat" "$out" 2>/dev/null; then
    pass "$client: TOML config written with a valid semidx entry"
  else
    fail "$client: written config missing semidx entry ($out)"
  fi
}

# print_only_client <client> <grep-pattern>: a client with no safe in-place
# merge must PRINT a snippet matching the pattern and refuse --apply.
print_only_client() {
  local client="$1" pat="$2"
  # Capture stdout+stderr: log noise (e.g. extract: libreoffice) must not hide the
  # snippet, and dumping the capture on miss makes flaky runner fails diagnosable.
  local out
  out="$(semidx mcp install --client "$client" 2>&1 || true)"
  if printf '%s' "$out" | grep -q "$pat"; then
    pass "$client: prints the expected snippet"
  else
    fail "$client: did not print the expected snippet"
    printf '%s\n' "$out" | sed 's/^/     /' >&2
  fi
  if semidx mcp install --client "$client" --config-file "$(mktemp -d)/x" --apply >/dev/null 2>&1; then
    fail "$client: --apply should be refused (print-only)"
  else
    pass "$client: --apply is correctly refused (print-only)"
  fi
}

# verify_claude_native: best-effort — if `claude` is installed, install to a
# project .mcp.json and confirm `claude mcp list` reports semidx.
verify_claude_native() {
  command -v claude >/dev/null 2>&1 || { skip "claude CLI not installed — native check"; return; }
  local dir; dir="$(mktemp -d)"
  ( cd "$dir" && semidx mcp install --client claude-code --apply >/dev/null 2>&1 ) || true
  if ( cd "$dir" && claude mcp list 2>/dev/null | grep -qi semidx ); then
    pass "claude mcp list reports the semidx server"
  else
    skip "claude mcp list did not report semidx (may need auth/config) — schema check still covers it"
  fi
}

# install_skill: verify `semidx skills install` writes the skill file.
install_skill() {
  local dir; dir="$(mktemp -d)"
  if semidx skills install --dir "$dir" >/dev/null 2>&1 \
    && [ -f "$dir/semantic-search/SKILL.md" ] \
    && [ -f "$dir/workspace-agent/SKILL.md" ]; then
    pass "semidx skills install wrote semantic-search and workspace-agent skills"
  else
    fail "semidx skills install did not produce the skill files"
  fi
}

# summary: print totals and exit non-zero if any hard failure occurred.
summary() {
  log "SUMMARY"
  printf '  passed=%d  failed=%d  skipped=%d\n' "$PASS" "$FAIL" "$SKIP"
  [ "$FAIL" -eq 0 ] || { printf '\n\033[31mHARNESS FAILED\033[0m\n'; exit 1; }
  printf '\n\033[32mHARNESS PASSED\033[0m\n'
}

# install_all_clients: the wiring gate common to every variation — every client's
# config is written correctly and merges preserve neighbors.
install_all_clients() {
  log "mcp install — write + verify each agent client's config"
  local mcpservers='c.mcpServers.semidx.command && c.mcpServers.semidx.args[0]==="mcp"'
  install_json_client claude-code    "$mcpservers"
  install_json_client claude-desktop "$mcpservers"
  install_json_client cursor         "$mcpservers"
  install_json_client windsurf       "$mcpservers"
  install_json_client gemini-cli     "$mcpservers"
  install_json_client antigravity    "$mcpservers"
  install_json_client copilot        "$mcpservers"
  install_json_client vscode         'c.servers.semidx.command && c.servers.semidx.args[0]==="mcp"'
  install_json_client opencode       'c.mcp.semidx.command.includes("mcp") && c.mcp.semidx.type==="local"'
  install_json_client crush          'c.mcp.semidx.type==="stdio" && c.mcp.semidx.args[0]==="mcp"'
  install_toml_client codex '\[mcp_servers.semidx\]'
  print_only_client cagent 'type: mcp'
  install_preserves_others cursor "$mcpservers"
  verify_claude_native
  install_skill
}
