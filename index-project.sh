#!/usr/bin/env bash
# Memory-capped indexing sandbox: runs the indexer inside a Docker container
# limited to 512MB RAM so a runaway indexing job can never freeze the host.
#
# Usage: ./index-project.sh <project_path> [model]
#
# Embedding credentials are taken from the environment (EMBED_API_KEY,
# GEMINI_API_KEY, ...) and passed through to the container. No secrets live
# in this script. Example:
#   GEMINI_API_KEY=... ./index-project.sh ~/projects/myapp gemini-embedding-2
set -euo pipefail

if [ $# -lt 1 ]; then
  echo "Usage: ./index-project.sh <project_path> [model]" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_PATH=$(realpath "$1")
MODEL="${2:-bge-m3}"

echo "Starting sandboxed indexing (RAM cap: 512MB)"
echo "Project: $PROJECT_PATH"
echo "Model:   $MODEL"

# Static build so the binary runs on the Alpine (musl) container
cd "$SCRIPT_DIR"
CGO_ENABLED=0 GOOS=linux go build -o semidx ./cmd/semidx

# --network host: reaches Postgres (localhost:55432) and Ollama (localhost:11434)
# -e VAR (no value): forwards the variable from the host env only when it is set
docker run --rm \
  -v "$SCRIPT_DIR":/app \
  -v "$PROJECT_PATH":"$PROJECT_PATH" \
  -w /app \
  --memory 512m \
  --memory-swap 512m \
  --network host \
  -e EMBED_PROVIDER -e EMBED_ENDPOINT -e EMBED_API_KEY \
  -e GEMINI_API_KEY -e GROQ_API_KEY -e OPENROUTER_API_KEY \
  -e OLLAMA_CLOUD_API_KEY -e OLLAMA_URL -e EMBED_PRIVACY \
  alpine sh -c "apk add --no-cache git >/dev/null && git config --global --add safe.directory '*' && /app/semidx index --project \"$PROJECT_PATH\" --model \"$MODEL\" --git --git-since=7.days --verbose"
