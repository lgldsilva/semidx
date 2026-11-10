#!/usr/bin/env bash
# Reproduce the agent A/B experiment (docs/research/agent-ab-semantic-search.md).
#
# Requirements: Go toolchain, ripgrep, python3, a local Ollama with the
# embedding model pulled (default bge-m3), and an authenticated `claude` CLI.
# The agent phase spends real model tokens (~US$ 4-5 with Sonnet for 150 runs).
#
# Usage: scripts/agent-ab/run.sh [commit] [work-dir]
#   AB_MODEL=sonnet  AB_EMBED_MODEL=bge-m3  AB_OLLAMA_URL=http://localhost:11434
#   AB_WORKERS=4     AB_SKIP_AGENTS=1 (retrieval only, no model spend)
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
repo="$(git -C "$here" rev-parse --show-toplevel)"
commit="${1:-HEAD}"
work="${2:-$(mktemp -d -t semidx-agent-ab.XXXXXX)}"

export AB_WORK="$work" AB_ROOT="$work/repo"
export AB_OLLAMA_URL="${AB_OLLAMA_URL:-http://localhost:11434}"
embed_model="${AB_EMBED_MODEL:-bge-m3}"
mkdir -p "$work/home"

cleanup() { git -C "$repo" worktree remove --force "$AB_ROOT" >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "== build semidx ($commit) into $work"
git -C "$repo" worktree add --detach "$AB_ROOT" "$commit" >/dev/null
(cd "$AB_ROOT" && go build -o "$work/semidx" ./cmd/semidx)
# Simulate an unfamiliar repository: agent guides would hand the layout to
# both conditions for free and hide what search contributes.
rm -f "$AB_ROOT/AGENTS.md" "$AB_ROOT/CLAUDE.md"
# The gold dataset must match the tree under test (relevant files exist there).
dataset="$AB_ROOT/testdata/eval/semidx-retrieval-v1.json"

# Isolated semidx environment: never reads the operator's config or index.
# Scoped to semidx only — the claude CLI still needs the operator's HOME/auth.
sx() {
  HOME="$work/home" SEMIDX_LOCAL_INDEX="$work/idx.db" \
    SEMIDX_OLLAMA_URL="$AB_OLLAMA_URL" OLLAMA_URL="$AB_OLLAMA_URL" "$work/semidx" "$@"
}

echo "== index with $embed_model"
(cd "$AB_ROOT" && sx index --local --model "$embed_model" . | tail -5)

echo "== retrieval benchmark"
for mode in keyword vector hybrid hybrid-graph; do
  strict=()
  [ "$mode" != keyword ] && strict=(--strict-semantic)
  (cd "$AB_ROOT" && sx --local bench retrieval --dataset "$dataset" \
    --mode "$mode" "${strict[@]}" --output "$work/semidx-$mode.json" >/dev/null)
done
python3 "$here/rg_baseline.py" "$dataset" "$AB_ROOT" "$work/rg.json"
python3 "$here/retrieval_table.py" "$work"

if [ "${AB_SKIP_AGENTS:-0}" = 1 ]; then
  echo "AB_SKIP_AGENTS=1: skipping the agent phase. Results in $work"
  exit 0
fi

echo "== agent A/B (${AB_MODEL:-sonnet}, 3 conditions x $(python3 -c "import json,sys;print(len(json.load(open(sys.argv[1]))['queries']))" "$dataset") queries)"
(cd "$here" && python3 run_agents.py "$dataset" "$work/agents.jsonl" "${AB_WORKERS:-4}" >"$work/agents.log")
python3 "$here/analyze.py" "$work/agents.jsonl"
echo "Results in $work"
