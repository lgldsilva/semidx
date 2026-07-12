#!/usr/bin/env bash
# Build the React admin SPA into internal/webui/dist for go:embed.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root/web"
if [[ ! -d node_modules ]]; then
  npm ci
fi
npm run build
echo "SPA written to internal/webui/dist"
